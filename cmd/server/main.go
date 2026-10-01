package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/chatgpt"
	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/opencode"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/Chibuoyimm/qa-agent/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	apiToken := os.Getenv("QA_API_TOKEN")
	workerToken := os.Getenv("QA_WORKER_TOKEN")
	connection := os.Getenv("DATABASE_URL")
	if apiToken == "" || workerToken == "" || connection == "" || apiToken == workerToken {
		return errors.New("DATABASE_URL and distinct QA_API_TOKEN / QA_WORKER_TOKEN values are required")
	}
	allowed, err := qa.ParseAllowedOrigins(os.Getenv("QA_ALLOWED_ORIGINS"))
	if err != nil {
		return err
	}
	addr := os.Getenv("QA_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	subscription, err := configureChatGPT(addr)
	if err != nil {
		return err
	}
	if subscription != nil {
		defer subscription.Close()
	}
	openCode, err := configureOpenCode(addr)
	if err != nil {
		return err
	}
	if openCode != nil {
		defer openCode.Close()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := pgxpool.New(ctx, connection)
	if err != nil {
		return err
	}
	defer db.Close()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.Ping(startup); err != nil {
		return err
	}
	if err := migrations.Apply(startup, db); err != nil {
		return err
	}
	proposals, err := planner.NewProviders([]planner.ProviderSettings{
		{Provider: "openai", Models: os.Getenv("QA_OPENAI_MODELS"), ManagedKey: os.Getenv("QA_OPENAI_API_KEY")},
		{Provider: "anthropic", Models: os.Getenv("QA_ANTHROPIC_MODELS"), ManagedKey: os.Getenv("QA_ANTHROPIC_API_KEY"), WorkspaceID: os.Getenv("QA_ANTHROPIC_WORKSPACE_ID")},
		{Provider: "google", Models: os.Getenv("QA_GEMINI_MODELS"), ManagedKey: os.Getenv("QA_GEMINI_API_KEY")},
	}, nil)
	if err != nil {
		return err
	}
	proposals.ChatGPT = subscription
	proposals.OpenCode = openCode
	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(qa.NewStore(db, allowed), proposals, repository.New(nil), apiToken, workerToken, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      100 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	server.RegisterOnShutdown(func() { logger.Info("server shut down") })
	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr <- server.Shutdown(shutdown)
	}()
	logger.Info("server listening", "addr", addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownErr
	}
	return err
}

func configureOpenCode(addr string) (*opencode.Client, error) {
	enabled := os.Getenv("QA_OPENCODE_ENABLED")
	if enabled == "" || enabled == "false" {
		return nil, nil
	}
	if enabled != "true" {
		return nil, errors.New("QA_OPENCODE_ENABLED must be true or false")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("OpenCode Go access requires a loopback QA_LISTEN_ADDR")
	}
	binary := os.Getenv("QA_OPENCODE_BINARY")
	if binary == "" {
		return nil, errors.New("set QA_OPENCODE_BINARY to the verified OpenCode 1.18.34 binary")
	}
	directory := os.Getenv("QA_OPENCODE_STORAGE_DIR")
	if directory == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, errors.New("set QA_OPENCODE_STORAGE_DIR to a protected local directory")
		}
		directory = filepath.Join(configDir, "qa-agent", "opencode-go")
	}
	return opencode.New(binary, directory)
}

func configureChatGPT(addr string) (*chatgpt.Client, error) {
	enabled := os.Getenv("QA_CHATGPT_ENABLED")
	if enabled == "" || enabled == "false" {
		return nil, nil
	}
	if enabled != "true" {
		return nil, errors.New("QA_CHATGPT_ENABLED must be true or false")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("ChatGPT plan access requires a loopback QA_LISTEN_ADDR")
	}
	directory := os.Getenv("QA_CHATGPT_STORAGE_DIR")
	if directory == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, errors.New("set QA_CHATGPT_STORAGE_DIR to a protected local directory")
		}
		directory = filepath.Join(configDir, "qa-agent", "chatgpt")
	}
	return chatgpt.New(directory, nil)
}
