package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid  = errors.New("invalid repository import")
	ErrUpstream = errors.New("repository provider unavailable")
	ErrTimeout  = errors.New("repository import timed out")
	ErrBusy     = errors.New("repository importer busy")
)

const (
	maxFiles         = 20
	maxBytes         = 40_000
	maxAPIRequests   = 64
	maxMetadataBytes = 8_000_000
)

type Input struct {
	Repository string   `json:"repository"`
	Ref        string   `json:"ref"`
	Role       string   `json:"role"`
	Paths      []string `json:"paths"`
}

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Snapshot struct {
	Repository    string `json:"repository"`
	Ref           string `json:"ref"`
	Role          string `json:"role"`
	CommitSHA     string `json:"commit_sha"`
	ContentSHA256 string `json:"content_sha256"`
	Files         []File `json:"files"`
	TotalBytes    int    `json:"total_bytes"`
}

type Client struct {
	http  *http.Client
	slots chan struct{}
}

// New fixes the API origin. A custom transport is useful for local tests; it cannot
// change the URLs or the redirect policy constructed by Fetch.
func New(transport http.RoundTripper) *Client {
	if transport == nil {
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.Proxy = nil
		transport = base
	}
	return &Client{
		http:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		slots: make(chan struct{}, 2),
	}
}

var (
	repositoryPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refPart        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	shaPattern     = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
	literalSecret  = regexp.MustCompile(`(?im)(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd|private[_-]?key)["'` + "`" + `]?\s*[=:]\s*["'` + "`" + `]([^"'` + "`" + `\r\n]{8,})["'` + "`" + `]`)
	knownSecret    = regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{20,}|-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----)`)
)

// Validate checks an import request and the optional GitHub token before provider access.
func Validate(in Input, token string) error {
	parts := strings.Split(in.Repository, "/")
	if len(parts) != 2 || len(parts[0]) > 39 || len(parts[1]) > 100 || !repositoryPart.MatchString(parts[0]) || !repositoryPart.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
		return fmt.Errorf("%w: repository must be owner/name", ErrInvalid)
	}
	if in.Ref == "" || len(in.Ref) > 200 || !refPart.MatchString(in.Ref) || strings.Contains(in.Ref, "..") || strings.Contains(in.Ref, "//") || strings.HasSuffix(in.Ref, "/") {
		return fmt.Errorf("%w: invalid ref", ErrInvalid)
	}
	if in.Role != "frontend" && in.Role != "backend" {
		return fmt.Errorf("%w: invalid role", ErrInvalid)
	}
	if len(in.Paths) == 0 || len(in.Paths) > maxFiles {
		return fmt.Errorf("%w: choose 1–20 paths", ErrInvalid)
	}
	if len(token) > 4096 {
		return fmt.Errorf("%w: invalid token", ErrInvalid)
	}
	for _, b := range []byte(token) {
		if b < '!' || b > '~' {
			return fmt.Errorf("%w: invalid token", ErrInvalid)
		}
	}
	seen := make(map[string]bool, len(in.Paths))
	for _, p := range in.Paths {
		if p == "" || len(p) > 1024 || !utf8.ValidString(p) || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") || seen[p] {
			return fmt.Errorf("%w: invalid or duplicate path", ErrInvalid)
		}
		seen[p] = true
		parts := strings.Split(p, "/")
		if len(parts) > 20 {
			return fmt.Errorf("%w: path is too deep", ErrInvalid)
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." || unsafeName(part) {
				return fmt.Errorf("%w: unsafe path", ErrInvalid)
			}
		}
	}
	return nil
}

func unsafeName(name string) bool {
	n := strings.ToLower(name)
	if n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasPrefix(n, ".env-") || n == ".npmrc" || n == ".pypirc" || n == "id_rsa" || n == "id_ed25519" || n == "credentials" || n == "secrets" {
		return true
	}
	for _, prefix := range []string{"credential.", "credentials.", "secret.", "secrets.", "service-account.", "service_account.", "serviceaccount.", "token.", "tokens."} {
		if strings.HasPrefix(n, prefix) && !strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, ".ts") && !strings.HasSuffix(n, ".js") && !strings.HasSuffix(n, ".py") {
			return true
		}
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".keystore", ".jks", ".env"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

func (c *Client) Fetch(ctx context.Context, in Input, token string) (Snapshot, error) {
	if err := Validate(in, token); err != nil {
		return Snapshot{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return Snapshot{}, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	base := "https://api.github.com/repos/" + in.Repository
	budget := requestBudget{}

	var commit struct {
		SHA    string `json:"sha"`
		Commit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		} `json:"commit"`
	}
	if err := c.get(ctx, base+"/commits/"+url.PathEscape(in.Ref), token, 1_000_000, &budget, &commit); err != nil {
		return Snapshot{}, err
	}
	if !shaPattern.MatchString(commit.SHA) || !shaPattern.MatchString(commit.Commit.Tree.SHA) {
		return Snapshot{}, fmt.Errorf("%w: invalid commit metadata", ErrUpstream)
	}

	files := make([]File, 0, len(in.Paths))
	total := 0
	trees := make(map[string]gitTree)
	for _, p := range in.Paths {
		sha := commit.Commit.Tree.SHA
		parts := strings.Split(p, "/")
		for i, part := range parts {
			tree, ok := trees[sha]
			if !ok {
				if err := c.get(ctx, base+"/git/trees/"+sha, token, 2_000_000, &budget, &tree); err != nil {
					return Snapshot{}, err
				}
				if tree.Truncated || tree.SHA != sha {
					return Snapshot{}, fmt.Errorf("%w: incomplete tree", ErrUpstream)
				}
				trees[sha] = tree
			}
			var entry *treeEntry
			for j := range tree.Tree {
				if tree.Tree[j].Path == part {
					entry = &tree.Tree[j]
					break
				}
			}
			if entry == nil {
				return Snapshot{}, fmt.Errorf("%w: selected file missing", ErrInvalid)
			}
			if !shaPattern.MatchString(entry.SHA) {
				return Snapshot{}, fmt.Errorf("%w: invalid tree entry", ErrUpstream)
			}
			if i < len(parts)-1 {
				if entry.Type != "tree" || entry.Mode != "040000" && entry.Mode != "40000" {
					return Snapshot{}, fmt.Errorf("%w: selected path is not a directory", ErrInvalid)
				}
				sha = entry.SHA
				continue
			}
			if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
				return Snapshot{}, fmt.Errorf("%w: selected path is not a regular file", ErrInvalid)
			}
			if entry.Size < 0 || entry.Size > maxBytes-total {
				return Snapshot{}, fmt.Errorf("%w: selected content exceeds 40,000 bytes", ErrInvalid)
			}
			var blob struct {
				SHA      string `json:"sha"`
				Size     int    `json:"size"`
				Encoding string `json:"encoding"`
				Content  string `json:"content"`
			}
			if err := c.get(ctx, base+"/git/blobs/"+entry.SHA, token, 80_000, &budget, &blob); err != nil {
				return Snapshot{}, err
			}
			if blob.SHA != entry.SHA || blob.Encoding != "base64" || blob.Size != entry.Size {
				return Snapshot{}, fmt.Errorf("%w: inconsistent blob", ErrUpstream)
			}
			content, err := base64.StdEncoding.DecodeString(blob.Content)
			if err != nil || len(content) != blob.Size {
				return Snapshot{}, fmt.Errorf("%w: invalid blob", ErrUpstream)
			}
			if !safeText(content) {
				return Snapshot{}, fmt.Errorf("%w: selected file contains binary data or a recognizable secret", ErrInvalid)
			}
			total += len(content)
			files = append(files, File{Path: p, Content: string(content)})
		}
	}
	hash := sha256.New()
	for _, file := range files {
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		hash.Write([]byte(file.Content))
		hash.Write([]byte{0})
	}
	return Snapshot{Repository: in.Repository, Ref: in.Ref, Role: in.Role, CommitSHA: commit.SHA, ContentSHA256: hex.EncodeToString(hash.Sum(nil)), Files: files, TotalBytes: total}, nil
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	Size int    `json:"size"`
	SHA  string `json:"sha"`
}

type gitTree struct {
	SHA       string      `json:"sha"`
	Tree      []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

func safeText(content []byte) bool {
	if !utf8.Valid(content) || knownSecret.Match(content) || literalSecret.Match(content) {
		return false
	}
	for _, b := range content {
		if (b < 0x20 && b != '\n' && b != '\r' && b != '\t') || b == 0x7f {
			return false
		}
	}
	return true
}

type requestBudget struct {
	requests int
	bytes    int64
}

func (c *Client) get(ctx context.Context, endpoint, token string, limit int64, budget *requestBudget, target any) error {
	if budget.requests >= maxAPIRequests {
		return fmt.Errorf("%w: selected paths exceed request limit", ErrInvalid)
	}
	remaining := int64(maxMetadataBytes) - budget.bytes
	if remaining <= 0 {
		return fmt.Errorf("%w: provider response budget exceeded", ErrUpstream)
	}
	if limit > remaining {
		limit = remaining
	}
	budget.requests++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: invalid request", ErrUpstream)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ErrTimeout
		}
		return ErrUpstream
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrUpstream
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ErrTimeout
		}
		return ErrUpstream
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("%w: provider response too large", ErrUpstream)
	}
	budget.bytes += int64(len(body))
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("%w: invalid provider response", ErrUpstream)
	}
	return nil
}
