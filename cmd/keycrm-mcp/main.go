// SPDX-License-Identifier: AGPL-3.0-only

// Команда keycrm-mcp — MCP-сервер для keyCRM.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// version заповнюється під час релізу через -ldflags "-X main.version=...".
var version = "dev"

const (
	// httpPath — шлях Streamable HTTP-ендпоінта.
	httpPath = "/mcp"
	// maxHTTPBody — найбільше тіло запиту MCP по HTTP.
	maxHTTPBody = 4 << 20
	// shutdownTimeout — скільки чекати завершення HTTP-запитів після сигналу.
	shutdownTimeout = 10 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "keycrm-mcp:", err)
		os.Exit(1)
	}
}

// run завантажує конфігурацію, створює клієнт keyCRM і сервер MCP та
// обслуговує stdio (за замовчуванням) або Streamable HTTP до скасування ctx.
// stdout належить stdio-транспорту, тому логи йдуть у stderr.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	cfg, err := config.Load(getenv, args)
	if errors.Is(err, flag.ErrHelp) {
		config.PrintUsage(stdout)
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.ShowVersion {
		if _, err := fmt.Fprintf(stdout, "keycrm-mcp %s\n", version); err != nil {
			return fmt.Errorf("виведення версії: %w", err)
		}
		return nil
	}

	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	srv, reg, err := newServer(cfg, log)
	if err != nil {
		return err
	}
	transport := "stdio"
	if cfg.HTTPAddr != "" {
		transport = "http"
	}
	log.Info("keycrm-mcp starting", "version", version, "transport", transport,
		"tools", len(reg.Specs()), "toolsets", strings.Join(cfg.Toolsets, ","), "read_only", cfg.ReadOnly)

	if cfg.HTTPAddr != "" {
		return serveHTTP(ctx, cfg, srv, log)
	}
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("stdio: %w", err)
	}
	return nil
}

// newServer створює клієнт keyCRM і сервер MCP з усіма групами інструментів.
func newServer(cfg config.Config, log *slog.Logger) (*mcp.Server, *server.Registry, error) {
	client, err := keycrm.New(keycrm.Config{
		APIKey:    cfg.APIKey,
		BaseURL:   cfg.BaseURL,
		RateLimit: cfg.RateLimit,
		Logger:    log,
	})
	if err != nil {
		return nil, nil, err
	}
	return server.New(cfg, server.Deps{
		KeyCRM:  client,
		Upload:  keycrm.UploadConfig{AllowPrivate: cfg.AllowPrivateURLs, UploadDir: cfg.UploadDir},
		Log:     log,
		Version: version,
	}, tools.All())
}

// httpHandler повертає обробник Streamable HTTP на httpPath, захищений Bearer-токеном.
func httpHandler(srv *mcp.Server, token string, log *slog.Logger) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Logger:              log,
		MaxRequestBodyBytes: maxHTTPBody,
	})
	mux := http.NewServeMux()
	mux.Handle(httpPath, requireBearer(token, h))
	return mux
}

// requireBearer пропускає лише запити з "Authorization: Bearer <token>";
// токен порівнюється за сталий час. Інакше — 401.
func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || len(want) == 0 ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="keycrm-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveHTTP обслуговує Streamable HTTP до скасування ctx, потім чекає
// завершення поточних запитів.
func serveHTTP(ctx context.Context, cfg config.Config, srv *mcp.Server, log *slog.Logger) error {
	hs := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpHandler(srv, cfg.HTTPToken, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("listening", "addr", cfg.HTTPAddr, "path", httpPath)

	select {
	case err := <-errc:
		return fmt.Errorf("http: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}
