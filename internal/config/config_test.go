// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"flag"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

// env повертає getenv поверх мапи.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadRequiresAPIKey(t *testing.T) {
	_, err := Load(env(nil), nil)
	if err == nil || !strings.Contains(err.Error(), "KEYCRM_API_KEY") {
		t.Fatalf("очікувалась помилка про KEYCRM_API_KEY, отримано %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"KEYCRM_API_KEY": "k"}), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !slices.Equal(cfg.Toolsets, []string{"all"}) {
		t.Errorf("Toolsets = %v, очікувано [all]", cfg.Toolsets)
	}
	if cfg.RateLimit != 20 {
		t.Errorf("RateLimit = %d, очікувано 20", cfg.RateLimit)
	}
	if cfg.BaseURL != "https://openapi.keycrm.app/v1" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.ReadOnly || cfg.HTTPAddr != "" {
		t.Errorf("неочікувані значення за замовчуванням: %+v", cfg)
	}
}

func TestLoadEnv(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"KEYCRM_API_KEY":            "k",
		"KEYCRM_API_URL":            "http://localhost:9999/v1",
		"KEYCRM_RATE_LIMIT":         "10",
		"KEYCRM_TOOLSETS":           " Orders, reference ,",
		"KEYCRM_READ_ONLY":          "true",
		"KEYCRM_LOG_LEVEL":          "debug",
		"KEYCRM_UPLOAD_DIR":         "/tmp/up",
		"KEYCRM_ALLOW_PRIVATE_URLS": "1",
	}), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != "http://localhost:9999/v1" || cfg.RateLimit != 10 || !cfg.ReadOnly ||
		cfg.LogLevel != slog.LevelDebug || cfg.UploadDir != "/tmp/up" || !cfg.AllowPrivateURLs {
		t.Errorf("env розібрано неправильно: %+v", cfg)
	}
	if !slices.Equal(cfg.Toolsets, []string{"orders", "reference"}) {
		t.Errorf("Toolsets = %v", cfg.Toolsets)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	cases := map[string]string{
		"KEYCRM_RATE_LIMIT":         "0",
		"KEYCRM_READ_ONLY":          "maybe",
		"KEYCRM_LOG_LEVEL":          "loud",
		"KEYCRM_ALLOW_PRIVATE_URLS": "yes please",
		"KEYCRM_API_URL":            "ftp://example.com",
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(map[string]string{"KEYCRM_API_KEY": "k", name: val}), nil)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("очікувалась помилка з назвою %s, отримано %v", name, err)
			}
		})
	}
}

func TestFlagsOverrideEnv(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"KEYCRM_API_KEY":        "k",
		"KEYCRM_TOOLSETS":       "catalog",
		"KEYCRM_READ_ONLY":      "false",
		"KEYCRM_MCP_HTTP_TOKEN": "secret",
	}), []string{"--read-only", "--toolsets", "orders,reference", "--http", "127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.ReadOnly {
		t.Error("--read-only не перекрив KEYCRM_READ_ONLY")
	}
	if !slices.Equal(cfg.Toolsets, []string{"orders", "reference"}) {
		t.Errorf("Toolsets = %v", cfg.Toolsets)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.HTTPToken != "secret" {
		t.Errorf("HTTP: %q %q", cfg.HTTPAddr, cfg.HTTPToken)
	}
}

func TestReadOnlyFlagFalseOverridesEnv(t *testing.T) {
	cfg, err := Load(env(map[string]string{"KEYCRM_API_KEY": "k", "KEYCRM_READ_ONLY": "true"}), []string{"--read-only=false"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReadOnly {
		t.Error("--read-only=false має перекрити env")
	}
}

func TestHTTPRequiresToken(t *testing.T) {
	_, err := Load(env(map[string]string{"KEYCRM_API_KEY": "k"}), []string{"--http", ":8080"})
	if err == nil || !strings.Contains(err.Error(), "KEYCRM_MCP_HTTP_TOKEN") {
		t.Fatalf("очікувалась помилка про KEYCRM_MCP_HTTP_TOKEN, отримано %v", err)
	}
}

func TestVersionSkipsValidation(t *testing.T) {
	cfg, err := Load(env(nil), []string{"--version"})
	if err != nil || !cfg.ShowVersion {
		t.Fatalf("--version: cfg=%+v err=%v", cfg, err)
	}
}

func TestHelpReturnsErrHelp(t *testing.T) {
	_, err := Load(env(nil), []string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("очікувався flag.ErrHelp, отримано %v", err)
	}
}

func TestUnknownFlag(t *testing.T) {
	if _, err := Load(env(map[string]string{"KEYCRM_API_KEY": "k"}), []string{"--nope"}); err == nil {
		t.Fatal("очікувалась помилка для невідомого прапорця")
	}
}
