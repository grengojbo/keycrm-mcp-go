// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

func TestIsDisallowedIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"198.18.0.1", true},
		{"224.0.0.1", true},
		{"255.255.255.255", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:8.8.8.8", true},
		{"64:ff9b::a00:1", true},
		{"::", true},
		{"8.8.8.8", false},
		{"2a00:1450::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := IsDisallowedIP(netip.MustParseAddr(tt.ip)); got != tt.want {
				t.Errorf("IsDisallowedIP(%s) = %v, очікується %v", tt.ip, got, tt.want)
			}
		})
	}
	if !IsDisallowedIP(netip.Addr{}) {
		t.Error("невалідна адреса має бути заборонена")
	}
}

// fakeKeyCRM емулює keyCRM і віддає файл за шляхом /file.
// uploads рахує успішні завантаження в /storage/upload.
type fakeKeyCRM struct {
	srv      *httptest.Server
	uploads  int
	gotName  string
	gotBytes int
	gotAuth  string
}

func newFakeKeyCRM(t *testing.T, file http.HandlerFunc) *fakeKeyCRM {
	t.Helper()
	f := &fakeKeyCRM{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/storage/upload":
			fh, hdr, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			b, _ := io.ReadAll(fh)
			f.uploads++
			f.gotName = hdr.Filename
			f.gotBytes = len(b)
			f.gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":1,"url":"https://cdn.example/f"}`)
		case "/file":
			if f.gotAuth = r.Header.Get("Authorization"); f.gotAuth != "" {
				http.Error(w, "ключ API не має потрапляти на сторонні адреси", http.StatusBadRequest)
				return
			}
			file(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeKeyCRM) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{APIKey: "k", BaseURL: f.srv.URL + "/v1", Limiter: rate.NewLimiter(rate.Inf, 1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func serveBytes(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) }
}

func TestUploadRejectsNonHTTP(t *testing.T) {
	f := newFakeKeyCRM(t, serveBytes(nil))
	c := f.client(t)
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://x", "//example.com/x", "not a url"} {
		if _, err := c.UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true}, u, ""); err == nil {
			t.Errorf("%q: очікувалась помилка", u)
		}
	}
	if f.uploads != 0 {
		t.Errorf("завантажень = %d, очікується 0", f.uploads)
	}
}

func TestUploadBlocksPrivateAtDial(t *testing.T) {
	f := newFakeKeyCRM(t, serveBytes([]byte("hello")))
	c := f.client(t)
	url := f.srv.URL + "/file"

	_, err := c.UploadFromURL(context.Background(), UploadConfig{}, url, "a.txt")
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("помилка = %v, очікується private address", err)
	}
	if f.uploads != 0 {
		t.Fatalf("завантажень = %d, очікується 0", f.uploads)
	}

	got, err := c.UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true}, url, "a.txt")
	if err != nil {
		t.Fatalf("AllowPrivate: %v", err)
	}
	if !strings.Contains(string(got), `"url"`) {
		t.Errorf("відповідь = %s", got)
	}
	if f.uploads != 1 || f.gotName != "a.txt" || f.gotBytes != 5 || f.gotAuth != "Bearer k" {
		t.Errorf("завантажено %d раз, файл %q, %d байт, auth %q", f.uploads, f.gotName, f.gotBytes, f.gotAuth)
	}
}

func TestUploadFilenameFromURL(t *testing.T) {
	f := newFakeKeyCRM(t, serveBytes([]byte("x")))
	c := f.client(t)
	if _, err := c.UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true}, f.srv.URL+"/file?a=1", ""); err != nil {
		t.Fatalf("UploadFromURL: %v", err)
	}
	if f.gotName != "file" {
		t.Errorf("ім'я файлу = %q, очікується file", f.gotName)
	}
}

func TestUploadBlocksRedirectToPrivate(t *testing.T) {
	f := newFakeKeyCRM(t, serveBytes([]byte("secret")))
	c := f.client(t)

	// «Зовнішній» сервер теж слухає loopback, тож дозволяємо саме його адресу,
	// а решту адрес перевіряємо звичайним правилом.
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.srv.URL+"/file", http.StatusFound)
	}))
	t.Cleanup(external.Close)
	allowed := netip.MustParseAddrPort(strings.TrimPrefix(external.URL, "http://"))

	cfg := UploadConfig{checkAddr: func(ap netip.AddrPort) error {
		if ap == allowed {
			return nil
		}
		return checkAddr(ap)
	}}
	_, err := c.UploadFromURL(context.Background(), cfg, external.URL+"/start", "a.txt")
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("помилка = %v, очікується private address", err)
	}
	if f.uploads != 0 {
		t.Errorf("завантажень = %d, очікується 0", f.uploads)
	}
}

func TestUploadRedirectLimit(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+r.URL.Path+"x", http.StatusFound) //nolint:gosec // тестовий сервер редіректить сам на себе
	}))
	t.Cleanup(srv.Close)
	f := newFakeKeyCRM(t, serveBytes(nil))
	_, err := f.client(t).UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true}, srv.URL+"/r", "a")
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("помилка = %v, очікується обмеження редіректів", err)
	}
}

func TestUploadStreamsSizeLimit(t *testing.T) {
	const limit = 1 << 10
	var written int64
	f := newFakeKeyCRM(t, func(w http.ResponseWriter, _ *http.Request) {
		// Без Content-Length: пишемо частинами з Flush, щоб відповідь була chunked.
		chunk := make([]byte, 256)
		for written <= limit*4 {
			n, err := w.Write(chunk)
			written += int64(n)
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	_, err := f.client(t).UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true, MaxBytes: limit}, f.srv.URL+"/file", "big")
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("помилка = %v, очікується larger than", err)
	}
	if f.uploads != 0 {
		t.Errorf("завантажень = %d, очікується 0", f.uploads)
	}
}

func TestUploadRejectsLargeContentLength(t *testing.T) {
	f := newFakeKeyCRM(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "2048")
		_, _ = w.Write(make([]byte, 2048))
	})
	_, err := f.client(t).UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true, MaxBytes: 1024}, f.srv.URL+"/file", "big")
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("помилка = %v, очікується larger than", err)
	}
}

func TestUploadSourceHTTPError(t *testing.T) {
	f := newFakeKeyCRM(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	_, err := f.client(t).UploadFromURL(context.Background(), UploadConfig{AllowPrivate: true}, f.srv.URL+"/file", "a")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("помилка = %v, очікується статус 404", err)
	}
}

func TestUploadLocalOutsideDir(t *testing.T) {
	f := newFakeKeyCRM(t, serveBytes(nil))
	c := f.client(t)
	ctx := context.Background()

	if _, err := c.UploadLocal(ctx, UploadConfig{}, "/etc/hosts", ""); err == nil || !strings.Contains(err.Error(), "local uploads are disabled") {
		t.Fatalf("UploadDir порожній: помилка = %v", err)
	}

	root := t.TempDir()
	dir := filepath.Join(root, "uploads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "secret.txt")
	inside := filepath.Join(dir, "photo.jpg")
	for p, data := range map[string]string{outside: "secret", inside: "jpeg"} {
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	cfg := UploadConfig{UploadDir: dir}

	for _, p := range []string{"../secret.txt", outside, filepath.Join(dir, "..", "secret.txt"), "link.txt", filepath.Join(dir, "link.txt"), ".", "missing.jpg"} {
		if _, err := c.UploadLocal(ctx, cfg, p, ""); err == nil {
			t.Errorf("%q: очікувалась помилка", p)
		}
	}
	if f.uploads != 0 {
		t.Fatalf("завантажень = %d, очікується 0", f.uploads)
	}

	for _, p := range []string{"photo.jpg", inside} {
		if _, err := c.UploadLocal(ctx, cfg, p, ""); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if f.uploads != 2 || f.gotName != "photo.jpg" || f.gotBytes != 4 {
		t.Errorf("завантажено %d раз, файл %q, %d байт", f.uploads, f.gotName, f.gotBytes)
	}
}

func TestUploadLocalSizeLimit(t *testing.T) {
	f := newFakeKeyCRM(t, serveBytes(nil))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.client(t).UploadLocal(context.Background(), UploadConfig{UploadDir: dir, MaxBytes: 1024}, "big.bin", "")
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("помилка = %v, очікується larger than", err)
	}
}

func TestUploadKeyCRMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"invalid","errors":{"file":["too big"]}}`)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "k", BaseURL: srv.URL, Limiter: rate.NewLimiter(rate.Inf, 1)})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = c.UploadLocal(context.Background(), UploadConfig{UploadDir: dir}, "a.txt", "")
	var ke *Error
	if !errors.As(err, &ke) || ke.Fields["file"][0] != "too big" {
		t.Fatalf("помилка = %v, очікується *Error з полем file", err)
	}
}
