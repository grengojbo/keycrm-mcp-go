// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// newTestClient створює клієнт, спрямований на тестовий сервер, з лімітером без обмежень.
func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "k", BaseURL: srv.URL, Limiter: rate.NewLimiter(rate.Inf, 1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

// doReq виконує запит через клієнт і повертає статус відповіді.
func doReq(ctx context.Context, t *testing.T, c *Client, method, url, body string) (int, error) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("очікувалась помилка без APIKey")
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr, ok := c.HTTP.Transport.(*transport)
	if !ok {
		t.Fatalf("транспорт %T, очікується *transport", c.HTTP.Transport)
	}
	if got, want := tr.limiter.Limit(), rate.Every(time.Minute/20); got != want {
		t.Errorf("ліміт = %v, очікується %v", got, want)
	}
	if c.API == nil {
		t.Error("API не ініціалізовано")
	}
}

func TestBearerHeader(t *testing.T) {
	var got string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	})
	resp, err := c.API.GetListOfCustomFieldsWithResponse(context.Background(), nil)
	if err != nil {
		t.Fatalf("запит: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("статус = %d", resp.StatusCode())
	}
	if got != "Bearer k" {
		t.Errorf("Authorization = %q, очікується %q", got, "Bearer k")
	}
}

func TestRateLimitQueues(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "k", BaseURL: srv.URL, Limiter: rate.NewLimiter(rate.Every(time.Hour), 2)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := doReq(ctx, t, c, http.MethodGet, srv.URL+"/x", "")
		cancel()
		if i < 2 && err != nil {
			t.Fatalf("запит %d: %v", i+1, err)
		}
		if i == 2 && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("запит 3: помилка %v, очікується context.DeadlineExceeded", err)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("сервер отримав %d запитів, очікується 2", n)
	}
}

func TestRetryOn429WithRetryAfter(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	start := time.Now()
	status, err := doReq(context.Background(), t, c, http.MethodPost, srv.URL+"/order", `{"a":1}`)
	if err != nil {
		t.Fatalf("запит: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("статус = %d", status)
	}
	if time.Since(start) < time.Second {
		t.Error("Retry-After: 1 не дотримано")
	}
	if len(bodies) != 2 {
		t.Fatalf("запитів = %d, очікується 2", len(bodies))
	}
	if bodies[0] != `{"a":1}` || bodies[1] != bodies[0] {
		t.Errorf("тіла запитів = %q, очікуються однакові", bodies)
	}
}

func TestGiveUpAfter3Retries(t *testing.T) {
	var hits atomic.Int32
	c, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"Too Many Attempts."}`)
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/order", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		t.Fatalf("запит: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	err = CheckResponse(resp.StatusCode, body)
	var ke *Error
	if !errors.As(err, &ke) || ke.Status != http.StatusTooManyRequests {
		t.Fatalf("помилка = %v, очікується *Error{Status:429}", err)
	}
	if n := hits.Load(); n != 4 {
		t.Errorf("запитів = %d, очікується 4", n)
	}
}

func TestGETRetriesOnceOn5xx(t *testing.T) {
	var hits atomic.Int32
	c, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	status, err := doReq(context.Background(), t, c, http.MethodGet, srv.URL+"/order", "")
	if err != nil {
		t.Fatalf("запит: %v", err)
	}
	if status != http.StatusBadGateway {
		t.Errorf("статус = %d", status)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("запитів = %d, очікується 2", n)
	}
}

func TestPOSTDoesNotRetryOn5xx(t *testing.T) {
	var hits atomic.Int32
	c, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := doReq(context.Background(), t, c, http.MethodPost, srv.URL+"/order", `{}`); err != nil {
		t.Fatalf("запит: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("запитів = %d, очікується 1", n)
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second}
	for n, w := range want {
		if got := backoff(n); got != w {
			t.Errorf("backoff(%d) = %v, очікується %v", n, got, w)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
		ok     bool
	}{
		{"секунди", "7", 7 * time.Second, true},
		{"нуль", "0", 0, true},
		{"дата", now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second, true},
		{"понад межу", "3600", maxRetryWait, true},
		{"порожньо", "", 0, false},
		{"сміття", "abc", 0, false},
		{"відʼємне", "-5", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := retryAfter(tt.header, now)
			if got != tt.want || ok != tt.ok {
				t.Errorf("retryAfter(%q) = %v, %v; очікується %v, %v", tt.header, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestBaseURLPathKept(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "k", BaseURL: srv.URL + "/v1", Limiter: rate.NewLimiter(rate.Inf, 1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.API.GetListOfCustomFieldsWithResponse(context.Background(), nil); err != nil {
		t.Fatalf("запит: %v", err)
	}
	if path != "/v1/custom-fields" {
		t.Errorf("шлях = %q, очікується /v1/custom-fields", path)
	}
}

// Таймаут діє на кожну спробу окремо: очікування в черзі лімітера його не з'їдає,
// а повільна відповідь — перериває.
func TestTimeoutPerAttempt(t *testing.T) {
	var slow atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			select {
			case <-time.After(time.Second):
			case <-r.Context().Done():
			}
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "k", BaseURL: srv.URL, Limiter: rate.NewLimiter(rate.Every(150*time.Millisecond), 1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.HTTP.Transport.(*transport).timeout = 100 * time.Millisecond

	for i := range 2 {
		if _, err := doReq(context.Background(), t, c, http.MethodPost, srv.URL+"/x", "{}"); err != nil {
			t.Fatalf("запит %d у черзі лімітера: %v", i+1, err)
		}
	}
	slow.Store(true)
	_, err = doReq(context.Background(), t, c, http.MethodPost, srv.URL+"/x", "{}")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("помилка = %v, очікується context.DeadlineExceeded", err)
	}
}
