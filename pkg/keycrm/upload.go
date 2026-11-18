// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultUploadMaxBytes — найбільший розмір файлу для завантаження.
	DefaultUploadMaxBytes = 50 << 20
	// maxRedirects — скільки редіректів дозволено під час завантаження за URL.
	maxRedirects = 5
)

// UploadConfig — правила завантаження файлів.
type UploadConfig struct {
	// AllowPrivate дозволяє завантаження з приватних і локальних адрес
	// (KEYCRM_ALLOW_PRIVATE_URLS) — лише для локальних тестів.
	AllowPrivate bool
	// UploadDir — каталог, з якого дозволено завантажувати локальні файли
	// (KEYCRM_UPLOAD_DIR). Порожній — локальні файли вимкнено.
	UploadDir string
	// MaxBytes — найбільший розмір файлу; за замовчуванням DefaultUploadMaxBytes.
	MaxBytes int64

	// checkAddr замінює перевірку адреси з'єднання (лише для тестів).
	checkAddr func(netip.AddrPort) error
}

func (cfg UploadConfig) maxBytes() int64 {
	if cfg.MaxBytes > 0 {
		return cfg.MaxBytes
	}
	return DefaultUploadMaxBytes
}

// disallowedPrefixes — діапазони, яких немає серед методів netip.Addr.
var disallowedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // «ця мережа»
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),    // службові призначення IETF
	netip.MustParsePrefix("192.0.2.0/24"),    // документація
	netip.MustParsePrefix("198.18.0.0/15"),   // тестування мереж
	netip.MustParsePrefix("198.51.100.0/24"), // документація
	netip.MustParsePrefix("203.0.113.0/24"),  // документація
	netip.MustParsePrefix("240.0.0.0/4"),     // зарезервовано, включно з broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: веде на довільні IPv4, зокрема приватні
	netip.MustParsePrefix("64:ff9b:1::/48"),  // локальний NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // документація
}

// IsDisallowedIP повідомляє, чи заборонено з'єднання з адресою під час
// завантаження за URL: loopback, приватні, link-local, CGNAT, 198.18/15,
// multicast, broadcast, 0/8, IPv6 ULA і link-local, IPv4-mapped IPv6 тощо.
func IsDisallowedIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Is4In6() {
		// IPv4-mapped IPv6 забороняємо цілком: інакше легко обійти перевірку IPv4.
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, p := range disallowedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// checkAddr — стандартна перевірка адреси з'єднання.
func checkAddr(ap netip.AddrPort) error {
	if IsDisallowedIP(ap.Addr()) {
		return fmt.Errorf("keycrm: upload: connection to private address %s is not allowed", ap.Addr())
	}
	return nil
}

// fetchClient будує http.Client для завантаження файлу за URL. Адреса
// перевіряється в net.Dialer.Control — уже після DNS-резолвінгу, на
// фактичній IP-адресі з'єднання, тож DNS rebinding не обходить перевірку.
// Проксі вимкнено: інакше перевірялася б адреса проксі, а не цільова.
func fetchClient(cfg UploadConfig) *http.Client {
	check := cfg.checkAddr
	if check == nil {
		check = checkAddr
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if cfg.AllowPrivate {
				return nil
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("keycrm: upload: unexpected dial address %q: %w", address, err)
			}
			return check(ap)
		},
	}
	return &http.Client{
		Timeout: uploadTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          1,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("keycrm: upload: stopped after %d redirects", maxRedirects)
			}
			// Адресу нового хоста знову перевірить Dialer.Control.
			return checkScheme(req.URL)
		},
	}
}

func checkScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("keycrm: upload: only http and https URLs are allowed, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("keycrm: upload: URL has no host")
	}
	return nil
}

// UploadFromURL завантажує файл за адресою rawURL (лише http/https, не з
// приватних адрес, до MaxBytes) і передає його в keyCRM (uploadStorageFile).
// Якщо filename порожній, ім'я береться з шляху URL. Повертає відповідь keyCRM.
func (c *Client) UploadFromURL(ctx context.Context, cfg UploadConfig, rawURL, filename string) (json.RawMessage, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: invalid URL: %w", err)
	}
	if err := checkScheme(u); err != nil {
		return nil, err
	}
	if filename == "" {
		filename = path.Base(u.Path)
	}
	name, err := cleanFilename(filename)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}
	resp, err := fetchClient(cfg).Do(req)
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("keycrm: upload: source returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	limit := cfg.maxBytes()
	if resp.ContentLength > limit {
		return nil, tooLarge(limit)
	}
	data, err := readLimited(resp.Body, limit)
	if err != nil {
		return nil, err
	}
	return c.sendFile(ctx, name, data)
}

// UploadLocal передає в keyCRM локальний файл. Дозволені лише файли всередині
// UploadDir (з урахуванням символьних посилань); відносний path рахується від
// UploadDir. Якщо filename порожній, береться ім'я файлу.
func (c *Client) UploadLocal(ctx context.Context, cfg UploadConfig, path, filename string) (json.RawMessage, error) {
	if cfg.UploadDir == "" {
		return nil, errors.New("keycrm: upload: local uploads are disabled (KEYCRM_UPLOAD_DIR is not set)")
	}
	rel, err := relInsideDir(cfg.UploadDir, path)
	if err != nil {
		return nil, err
	}
	if filename == "" {
		filename = filepath.Base(rel)
	}
	name, err := cleanFilename(filename)
	if err != nil {
		return nil, err
	}

	// os.Root відкриває файл без виходу за межі каталогу навіть якщо між
	// перевіркою й відкриттям хтось підмінить шлях символьним посиланням.
	root, err := os.OpenRoot(cfg.UploadDir)
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: open upload dir: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("keycrm: upload: %q is not a regular file", path)
	}
	limit := cfg.maxBytes()
	if st.Size() > limit {
		return nil, tooLarge(limit)
	}
	data, err := readLimited(f, limit)
	if err != nil {
		return nil, err
	}
	return c.sendFile(ctx, name, data)
}

// relInsideDir перетворює p на шлях відносно dir, розкривши символьні
// посилання, і відмовляє, якщо файл лежить поза dir.
func relInsideDir(dir, p string) (string, error) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("keycrm: upload: upload dir: %w", err)
	}
	if realDir, err = filepath.Abs(realDir); err != nil {
		return "", fmt.Errorf("keycrm: upload: upload dir: %w", err)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	realPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("keycrm: upload: %w", err)
	}
	if realPath, err = filepath.Abs(realPath); err != nil {
		return "", fmt.Errorf("keycrm: upload: %w", err)
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("keycrm: upload: %q is outside KEYCRM_UPLOAD_DIR", p)
	}
	return rel, nil
}

// cleanFilename лишає тільки ім'я файлу без каталогів.
func cleanFilename(name string) (string, error) {
	name = path.Base(filepath.ToSlash(strings.TrimSpace(name)))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", errors.New("keycrm: upload: filename is required")
	}
	return name, nil
}

func tooLarge(limit int64) error {
	return fmt.Errorf("keycrm: upload: file is larger than %d bytes", limit)
}

// readLimited читає не більше limit байт; якщо даних більше — помилка.
// Читається щонайбільше limit+1 байт, тож пам'ять не росте понад ліміт.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: read: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, tooLarge(limit)
	}
	return data, nil
}

// sendFile надсилає файл у keyCRM multipart-полем file (POST /storage/upload).
func (c *Client) sendFile(ctx context.Context, name string, data []byte) (json.RawMessage, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	h.Set("Content-Type", http.DetectContentType(data))
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}

	resp, err := c.uploadAPI.UploadStorageFileWithBodyWithResponse(ctx, mw.FormDataContentType(), bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("keycrm: upload: %w", err)
	}
	if err := CheckResponse(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	return json.RawMessage(resp.Body), nil
}
