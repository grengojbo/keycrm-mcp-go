// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	// max429Retries — скільки разів повторювати запит після 429.
	max429Retries = 3
	// max5xxRetries — скільки разів повторювати GET після 5xx.
	max5xxRetries = 1
	// maxRetryWait — найдовша пауза перед повтором.
	maxRetryWait = 60 * time.Second
	// retry5xxWait — пауза перед повтором GET після 5xx.
	retry5xxWait = time.Second

	userAgent = "keycrm-mcp-go"
)

// transport додає авторизацію, чекає на лімітер перед кожною спробою і
// повторює запити за правилами keyCRM.
type transport struct {
	base    http.RoundTripper
	limiter *rate.Limiter
	apiKey  string
	log     *slog.Logger
	// timeout — таймаут однієї спроби (з'єднання, заголовки й читання тіла).
	timeout time.Duration
}

// RoundTrip реалізує http.RoundTripper.
//
// 429 повторюється до max429Retries разів після Retry-After або експоненційної
// паузи; 5xx повторюється лише для GET, бо запис міг уже відбутися.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	var n429, n5xx int
	body := req.Body
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			var err error
			if body, err = rewindBody(req); err != nil {
				return nil, err
			}
		}
		if err := t.waitLimiter(ctx); err != nil {
			if body != nil {
				_ = body.Close()
			}
			return nil, err
		}

		resp, err := t.attempt(req, body)
		if err != nil {
			return nil, err
		}

		var wait time.Duration
		switch {
		case resp.StatusCode == http.StatusTooManyRequests && n429 < max429Retries:
			d, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now())
			if !ok {
				d = backoff(n429)
			}
			n429++
			wait = d
		case resp.StatusCode >= 500 && req.Method == http.MethodGet && n5xx < max5xxRetries:
			n5xx++
			wait = retry5xxWait
		default:
			return resp, nil
		}
		if req.Body != nil && req.GetBody == nil {
			// Тіло не можна перечитати — повтор неможливий.
			return resp, nil
		}

		drain(resp)
		t.log.LogAttrs(ctx, slog.LevelWarn, "keycrm retry",
			slog.String("method", req.Method),
			slog.String("path", req.URL.Path),
			slog.Int("status", resp.StatusCode),
			slog.Duration("wait", wait),
		)
		if err := sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("keycrm: wait before retry: %w", err)
		}
	}
}

// attempt виконує одну спробу з власним таймаутом, що діє до закриття тіла відповіді.
func (t *transport) attempt(req *http.Request, body io.ReadCloser) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	r := req.Clone(ctx)
	r.Body = body
	r.Header.Set("Authorization", "Bearer "+t.apiKey)
	r.Header.Set("User-Agent", userAgent)
	if r.Header.Get("Accept") == "" {
		r.Header.Set("Accept", "application/json")
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// rewindBody повертає свіжу копію тіла запиту для повторної відправки,
// не змінюючи сам запит.
func rewindBody(req *http.Request) (io.ReadCloser, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return req.Body, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("keycrm: rewind request body: %w", err)
	}
	return body, nil
}

// drain дочитує й закриває тіло відповіді, щоб з'єднання повернулося в пул.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// backoff — пауза перед n-м (з нуля) повтором без Retry-After: 5 с·2ⁿ, не більше 60 с.
func backoff(n int) time.Duration {
	d := 5 * time.Second
	for range n {
		d *= 2
		if d >= maxRetryWait {
			return maxRetryWait
		}
	}
	return d
}

// retryAfter розбирає заголовок Retry-After (секунди або HTTP-дата) і
// обмежує паузу maxRetryWait. ok=false, якщо заголовок відсутній або некоректний.
func retryAfter(h string, now time.Time) (time.Duration, bool) {
	if h == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(h); err == nil {
		d = max(at.Sub(now), 0)
	} else {
		return 0, false
	}
	return min(d, maxRetryWait), true
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// cancelOnClose скасовує контекст спроби, коли тіло відповіді закрито.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// waitLimiter чекає на дозвіл лімітера. Якщо дозволу не дочекатися до дедлайну
// контексту, rate повертає власну помилку одразу; її загортаємо в
// context.DeadlineExceeded, щоб викликач міг перевірити errors.Is.
func (t *transport) waitLimiter(ctx context.Context) error {
	err := t.limiter.Wait(ctx)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("keycrm: wait for rate limiter: %w", ctx.Err())
	}
	if _, ok := ctx.Deadline(); ok {
		return fmt.Errorf("keycrm: wait for rate limiter: %w (%w)", context.DeadlineExceeded, err)
	}
	return fmt.Errorf("keycrm: wait for rate limiter: %w", err)
}
