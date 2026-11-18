// SPDX-License-Identifier: AGPL-3.0-only

// Package keycrm — Go-клієнт офіційного OpenAPI keyCRM (https://openapi.keycrm.app/v1).
//
// Типи й методи всіх операцій генеруються з вендореного api.yaml у підпакет api;
// цей пакет додає до них спільний ліміт запитів, повтори, типізовані помилки,
// пагінацію та безпечне завантаження файлів.
package keycrm

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

const (
	// DefaultBaseURL — адреса публічного API keyCRM.
	DefaultBaseURL = "https://openapi.keycrm.app/v1"
	// DefaultRateLimit — ліміт keyCRM: запитів на хвилину на один API-ключ.
	DefaultRateLimit = 20
	// MaxPageSize — найбільше значення параметра limit, яке приймає keyCRM.
	MaxPageSize = 50

	// requestTimeout — таймаут однієї спроби звичайного запиту.
	requestTimeout = 15 * time.Second
	// uploadTimeout — таймаут завантаження файлу (джерело й keyCRM окремо).
	uploadTimeout = 60 * time.Second
)

// Config — налаштування клієнта.
type Config struct {
	// APIKey — ключ API keyCRM; обов'язковий.
	APIKey string
	// BaseURL — адреса API; за замовчуванням DefaultBaseURL.
	BaseURL string
	// RateLimit — запитів на хвилину; за замовчуванням DefaultRateLimit.
	RateLimit int
	// Limiter, якщо заданий, замінює лімітер, побудований з RateLimit (для тестів
	// і для спільного лімітера кількох клієнтів з одним ключем).
	Limiter *rate.Limiter
	// Logger — журнал повторів; за замовчуванням нічого не пише.
	Logger *slog.Logger
}

// Client — клієнт keyCRM. API — згенерований клієнт усіх операцій,
// HTTP — http.Client з лімітом і повторами, через який він ходить.
type Client struct {
	API  *api.ClientWithResponses
	HTTP *http.Client

	// uploadAPI ходить через той самий лімітер, але з довшим таймаутом спроби.
	uploadAPI *api.ClientWithResponses
}

// New створює клієнт. Усі запити одного клієнта ділять один лімітер, тож
// при перевищенні ліміту вони стають у чергу, а не падають.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("keycrm: API key is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.RateLimit < 0 {
		return nil, fmt.Errorf("keycrm: RateLimit must be positive, got %d", cfg.RateLimit)
	}
	if cfg.RateLimit == 0 {
		cfg.RateLimit = DefaultRateLimit
	}
	limiter := cfg.Limiter
	if limiter == nil {
		// burst 1: запити рівномірно розподіляються в межах хвилини, тож у будь-якому
		// хвилинному вікні їх не більше за ліміт keyCRM.
		limiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(cfg.RateLimit)), 1)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	tr := &transport{
		base:    http.DefaultTransport,
		limiter: limiter,
		apiKey:  cfg.APIKey,
		log:     log,
		timeout: requestTimeout,
	}
	// Таймаут застосовується до кожної спроби в транспорті, а не через
	// http.Client.Timeout: інакше час очікування в черзі лімітера рахувався б
	// у таймаут і запити падали б замість того, щоб чекати.
	hc := &http.Client{Transport: tr}

	apiClient, err := api.NewClientWithResponses(cfg.BaseURL, api.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("keycrm: create client: %w", err)
	}

	up := *tr
	up.timeout = uploadTimeout
	uploadAPI, err := api.NewClientWithResponses(cfg.BaseURL, api.WithHTTPClient(&http.Client{Transport: &up}))
	if err != nil {
		return nil, fmt.Errorf("keycrm: create upload client: %w", err)
	}
	return &Client{API: apiClient, HTTP: hc, uploadAPI: uploadAPI}, nil
}
