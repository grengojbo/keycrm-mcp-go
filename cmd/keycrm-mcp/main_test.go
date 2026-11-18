// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
)

func noEnv(string) string { return "" }

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"--version"}, noEnv, &out, &out); err != nil {
		t.Fatalf("run(--version) повернув помилку: %v", err)
	}
	if got, want := out.String(), "keycrm-mcp dev\n"; got != want {
		t.Fatalf("вивід = %q, очікувано %q", got, want)
	}
}

func TestHelpFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-h"}, noEnv, &out, &out); err != nil {
		t.Fatalf("run(-h): %v", err)
	}
	if !strings.Contains(out.String(), "-read-only") {
		t.Fatalf("довідка без прапорців: %q", out.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"--no-such-flag"}, noEnv, &out, &out); err == nil {
		t.Fatal("очікувалась помилка для невідомого прапорця")
	}
}

func TestMissingAPIKey(t *testing.T) {
	var out bytes.Buffer
	err := run(t.Context(), nil, noEnv, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "KEYCRM_API_KEY") {
		t.Fatalf("очікувалась помилка про KEYCRM_API_KEY, отримано %v", err)
	}
}

func TestHTTPRejectsMissingToken(t *testing.T) {
	cfg := config.Config{APIKey: "k", Toolsets: []string{"all"}}
	srv, _, err := newServer(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(httpHandler(srv, "s3cret-token", slog.New(slog.DiscardHandler)))
	t.Cleanup(ts.Close)

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	cases := []struct {
		name, auth string
		want       int
	}{
		{"без заголовка", "", http.StatusUnauthorized},
		{"інша схема", "Basic czNjcmV0LXRva2Vu", http.StatusUnauthorized},
		{"хибний токен", "Bearer wrong", http.StatusUnauthorized},
		{"порожній токен", "Bearer ", http.StatusUnauthorized},
		{"правильний токен", "Bearer s3cret-token", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+httpPath, strings.NewReader(initBody))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("статус %d, очікувано %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestHTTPModeRequiresToken(t *testing.T) {
	env := func(k string) string {
		if k == "KEYCRM_API_KEY" {
			return "k"
		}
		return ""
	}
	var out bytes.Buffer
	err := run(t.Context(), []string{"--http", "127.0.0.1:0"}, env, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "KEYCRM_MCP_HTTP_TOKEN") {
		t.Fatalf("очікувалась відмова старту без токена, отримано %v", err)
	}
}
