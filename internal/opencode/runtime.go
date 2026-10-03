package opencode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const RuntimeVersion = "1.18.34"

// Pinned V1 configuration: no inherited plugins, files, other provider keys or
// background model calls. StructuredOutput is added by the prompt API itself.
const runtimeConfig = `{"enabled_providers":["opencode-go"],"default_agent":"qa-proposal","permission":"deny","agent":{"qa-proposal":{"mode":"primary","prompt":"Complete the requested structured QA task only from the explicitly supplied evidence. Use StructuredOutput to return the result; do not inspect files or execute actions.","steps":1,"permission":{"*":"deny","StructuredOutput":"allow"}},"build":{"disable":true},"plan":{"disable":true},"general":{"disable":true},"explore":{"disable":true},"title":{"disable":true},"summary":{"disable":true},"compaction":{"disable":true}},"share":"disabled","autoupdate":false,"snapshot":false,"compaction":{"auto":false,"prune":false}}`

type runtimeProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	lock *os.File
	once sync.Once
}

// New starts a dedicated authenticated loopback server. It never reuses the
// user's OpenCode projects, configuration, sessions or provider credentials.
func New(binary, directory string) (*Client, error) {
	if !filepath.IsAbs(binary) {
		return nil, errors.New("QA_OPENCODE_BINARY must be an absolute path to OpenCode 1.18.34")
	}
	versionCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	version, err := exec.CommandContext(versionCtx, binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != RuntimeVersion {
		return nil, errors.New("OpenCode Go requires the verified OpenCode 1.18.34 binary; set QA_OPENCODE_BINARY")
	}
	return startRuntime(binary, directory, runtimeConfig)
}

func startRuntime(binary, directory, config string) (*Client, error) {
	if !filepath.IsAbs(directory) {
		return nil, ErrInvalid
	}
	for ancestor := filepath.Clean(directory); ; ancestor = filepath.Dir(ancestor) {
		if _, err := os.Lstat(filepath.Join(ancestor, ".git")); err == nil {
			return nil, ErrInvalid
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, ErrUnavailable
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, ErrUnavailable
	}
	// Reject any repository, including a repository reached through a symlink.
	for ancestor := directory; ; ancestor = filepath.Dir(ancestor) {
		if _, err := os.Lstat(filepath.Join(ancestor, ".git")); err == nil {
			return nil, ErrInvalid
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	lockFD, err := syscall.Open(filepath.Join(directory, "runtime.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	lock := os.NewFile(uintptr(lockFD), "runtime.lock")
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		lock.Close()
		return nil, ErrUnavailable
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, ErrBusy
	}
	runtime := &runtimeProcess{lock: lock, done: make(chan struct{})}
	failed := true
	defer func() {
		if failed {
			runtime.close()
		}
	}()
	if err := claimDirectory(directory); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, ErrUnavailable
	}
	for _, name := range []string{"home", "config", "data", "cache", "state", "workspace"} {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, ErrUnavailable
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return nil, ErrUnavailable
		}
	}
	entries, err := os.ReadDir(filepath.Join(directory, "workspace"))
	if err != nil || len(entries) != 0 {
		return nil, errors.New("OpenCode QA workspace must be empty")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, ErrUnavailable
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	passwordBytes := make([]byte, 32)
	if _, err := rand.Read(passwordBytes); err != nil {
		return nil, ErrUnavailable
	}
	password := hex.EncodeToString(passwordBytes)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	runtime.cmd = exec.Command(binary, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	runtime.cmd.Dir = filepath.Join(directory, "workspace")
	runtime.cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(directory, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(directory, "config"), "XDG_DATA_HOME=" + filepath.Join(directory, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(directory, "cache"), "XDG_STATE_HOME=" + filepath.Join(directory, "state"),
		"OPENCODE_SERVER_PASSWORD=" + password, "OPENCODE_SERVER_USERNAME=opencode",
		"OPENCODE_DISABLE_PROJECT_CONFIG=true", "OPENCODE_DISABLE_CLAUDE_CODE=true",
		"OPENCODE_DISABLE_DEFAULT_PLUGINS=true", "OPENCODE_CONFIG_CONTENT=" + config,
		"OPENCODE_PURE=true", "OPENCODE_DISABLE_EXTERNAL_SKILLS=true", "OPENCODE_DISABLE_LSP_DOWNLOAD=true",
		"OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=6000",
	}
	if err := runtime.cmd.Start(); err != nil {
		runtime.cmd = nil
		return nil, ErrUnavailable
	}
	go func() { _ = runtime.cmd.Wait(); close(runtime.done) }()
	client := &Client{baseURL: "http://" + address, password: password, gate: make(chan struct{}, 1), runtime: runtime,
		http: &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	startup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var health struct {
			Healthy bool   `json:"healthy"`
			Version string `json:"version"`
		}
		probe, cancel := context.WithTimeout(startup, time.Second)
		err := client.call(probe, http.MethodGet, "/global/health", nil, &health)
		cancel()
		if err == nil && health.Healthy && health.Version == RuntimeVersion {
			if err := client.recoverSessions(startup); err != nil {
				return nil, err
			}
			failed = false
			return client, nil
		}
		select {
		case <-startup.Done():
			return nil, ErrUnavailable
		case <-runtime.done:
			return nil, ErrUnavailable
		case <-ticker.C:
		}
	}
}

// An existing personal OpenCode directory must never be adopted implicitly.
func claimDirectory(directory string) error {
	path := filepath.Join(directory, "qa-agent-runtime")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	const owner = "qa-agent/opencode-go/v1\n"
	if errors.Is(err, syscall.ENOENT) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return ErrUnavailable
		}
		for _, entry := range entries {
			if entry.Name() != "runtime.lock" {
				return errors.New("OpenCode storage must be a new dedicated QA directory")
			}
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return ErrUnavailable
		}
		_, writeErr := file.WriteString(owner)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return ErrUnavailable
		}
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	file := os.NewFile(uintptr(fd), "qa-agent-runtime")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil || string(data) != owner {
		return ErrInvalid
	}
	return nil
}

func (c *Client) Close() {
	if c.runtime != nil {
		c.runtime.close()
	}
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func (r *runtimeProcess) close() {
	r.once.Do(func() {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Signal(os.Interrupt)
			select {
			case <-r.done:
			case <-time.After(3 * time.Second):
				_ = r.cmd.Process.Kill()
				<-r.done
			}
		}
		_ = syscall.Flock(int(r.lock.Fd()), syscall.LOCK_UN)
		_ = r.lock.Close()
	})
}
