// SPDX-License-Identifier: AGPL-3.0-only

// Package config збирає налаштування сервера зі змінних оточення й прапорців.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// Config — налаштування сервера. Сервер без стану: локальної бази немає.
type Config struct {
	// APIKey — ключ API keyCRM (KEYCRM_API_KEY), обов'язковий.
	APIKey string
	// BaseURL — адреса API (KEYCRM_API_URL).
	BaseURL string
	// RateLimit — запитів на хвилину (KEYCRM_RATE_LIMIT).
	RateLimit int
	// Toolsets — увімкнені групи інструментів (KEYCRM_TOOLSETS, --toolsets); "all" — усі.
	Toolsets []string
	// ReadOnly — реєструвати лише інструменти читання (KEYCRM_READ_ONLY, --read-only).
	ReadOnly bool
	// LogLevel — рівень логів у stderr (KEYCRM_LOG_LEVEL).
	LogLevel slog.Level
	// UploadDir — тека, з якої дозволено завантажувати локальні файли (KEYCRM_UPLOAD_DIR).
	UploadDir string
	// AllowPrivateURLs — дозволити завантаження з приватних адрес (KEYCRM_ALLOW_PRIVATE_URLS).
	AllowPrivateURLs bool
	// HTTPAddr — адреса Streamable HTTP (--http); порожня — stdio.
	HTTPAddr string
	// HTTPToken — Bearer-токен для HTTP-транспорту (KEYCRM_MCP_HTTP_TOKEN).
	HTTPToken string
	// ShowVersion — показати версію й вийти (--version); решта полів тоді не перевіряється.
	ShowVersion bool
}

// flags — прапорці командного рядка; nil-значення означає «не задано».
type flags struct {
	fs       *flag.FlagSet
	httpAddr *string
	readOnly *bool
	toolsets *string
	version  *bool
}

func newFlags() flags {
	fs := flag.NewFlagSet("keycrm-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return flags{
		fs:       fs,
		httpAddr: fs.String("http", "", "запустити Streamable HTTP на адресі (напр. 127.0.0.1:8080) замість stdio; потрібен KEYCRM_MCP_HTTP_TOKEN"),
		readOnly: fs.Bool("read-only", false, "реєструвати лише інструменти читання (перекриває KEYCRM_READ_ONLY)"),
		toolsets: fs.String("toolsets", "", "групи інструментів через кому або all (перекриває KEYCRM_TOOLSETS)"),
		version:  fs.Bool("version", false, "показати версію і вийти"),
	}
}

// PrintUsage виводить довідку з прапорців.
func PrintUsage(w io.Writer) {
	f := newFlags()
	f.fs.SetOutput(w)
	_, _ = fmt.Fprintln(w, "Використання: keycrm-mcp [прапорці]\nНалаштування — змінні оточення KEYCRM_* (див. README).")
	f.fs.PrintDefaults()
}

// Load читає змінні оточення через getenv і прапорці з args. Прапорці
// перекривають змінні оточення. Помилка називає змінну або прапорець і причину;
// для -h/--help повертається помилка, що містить flag.ErrHelp.
func Load(getenv func(string) string, args []string) (Config, error) {
	f := newFlags()
	if err := f.fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("прапорці: %w", err)
	}
	if f.fs.NArg() > 0 {
		return Config{}, fmt.Errorf("прапорці: зайві аргументи %q", f.fs.Args())
	}
	if *f.version {
		return Config{ShowVersion: true}, nil
	}
	set := map[string]bool{}
	f.fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })

	cfg := Config{
		APIKey:    strings.TrimSpace(getenv("KEYCRM_API_KEY")),
		BaseURL:   keycrm.DefaultBaseURL,
		RateLimit: keycrm.DefaultRateLimit,
		Toolsets:  splitList(getenv("KEYCRM_TOOLSETS")),
		LogLevel:  slog.LevelInfo,
		UploadDir: strings.TrimSpace(getenv("KEYCRM_UPLOAD_DIR")),
		HTTPToken: strings.TrimSpace(getenv("KEYCRM_MCP_HTTP_TOKEN")),
	}
	if cfg.APIKey == "" {
		return Config{}, errors.New("KEYCRM_API_KEY: не задано ключ API keyCRM (Налаштування → Загальні → API)")
	}
	if v := strings.TrimSpace(getenv("KEYCRM_API_URL")); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return Config{}, fmt.Errorf("KEYCRM_API_URL: очікується адреса http(s)://…, отримано %q", v)
		}
		cfg.BaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(getenv("KEYCRM_RATE_LIMIT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("KEYCRM_RATE_LIMIT: очікується ціле число більше 0, отримано %q", v)
		}
		cfg.RateLimit = n
	}
	var err error
	if cfg.ReadOnly, err = parseBool(getenv, "KEYCRM_READ_ONLY"); err != nil {
		return Config{}, err
	}
	if cfg.AllowPrivateURLs, err = parseBool(getenv, "KEYCRM_ALLOW_PRIVATE_URLS"); err != nil {
		return Config{}, err
	}
	if v := strings.TrimSpace(getenv("KEYCRM_LOG_LEVEL")); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("KEYCRM_LOG_LEVEL: очікується debug, info, warn або error, отримано %q", v)
		}
	}

	if set["read-only"] {
		cfg.ReadOnly = *f.readOnly
	}
	if set["toolsets"] {
		cfg.Toolsets = splitList(*f.toolsets)
	}
	if len(cfg.Toolsets) == 0 {
		cfg.Toolsets = []string{"all"}
	}
	cfg.HTTPAddr = strings.TrimSpace(*f.httpAddr)
	if cfg.HTTPAddr != "" && cfg.HTTPToken == "" {
		return Config{}, errors.New("--http: HTTP-транспорт потребує KEYCRM_MCP_HTTP_TOKEN (Bearer-токен для клієнтів)")
	}
	return cfg, nil
}

// parseBool розбирає логічну змінну оточення; порожня — false.
func parseBool(getenv func(string) string, name string) (bool, error) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: очікується true або false, отримано %q", name, v)
	}
	return b, nil
}

// splitList ділить список через кому, обрізає пробіли, переводить у нижній
// регістр і відкидає порожні елементи.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}
