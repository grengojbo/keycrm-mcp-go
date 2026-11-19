// SPDX-License-Identifier: AGPL-3.0-only

package files

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

func connect(t *testing.T, h http.HandlerFunc, upload keycrm.UploadConfig) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h), Upload: upload}, false)
}

// uploadHandler — фейковий keyCRM, що приймає POST /storage/upload і
// перевіряє ім'я та вміст файлу.
func uploadHandler(t *testing.T, calls *atomic.Int32, wantName, wantData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/storage/upload" {
			t.Errorf("запит %s %s", r.Method, r.URL.Path)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		defer func() { _ = f.Close() }()
		data, _ := io.ReadAll(f)
		if h.Filename != wantName || string(data) != wantData {
			t.Errorf("файл %q: %q", h.Filename, data)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":330}`)
	}
}

func TestUploadFileNeedsExactlyOneSource(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, keycrm.UploadConfig{})
	for name, args := range map[string]map[string]any{
		"жодного":   {},
		"обидва":    {"url": "https://example.com/a.pdf", "file_path": "a.pdf"},
		"пробіли":   {"url": "  ", "file_path": " "},
		"лише ім'я": {"filename": "a.pdf"},
	} {
		t.Run(name, func(t *testing.T) {
			res := server.CallTool(t, cs, "upload_file", args)
			if !res.IsError || !strings.Contains(server.ResultText(res), "exactly one") {
				t.Errorf("очікувалась помилка: %s", server.ResultText(res))
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("запитів до keyCRM: %d", calls.Load())
	}
}

func TestUploadFileLocalDisabledByDefault(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, keycrm.UploadConfig{})
	res := server.CallTool(t, cs, "upload_file", map[string]any{"file_path": "/etc/passwd"})
	if !res.IsError || !strings.Contains(server.ResultText(res), "local uploads are disabled") || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d: %s", res.IsError, calls.Load(), server.ResultText(res))
	}
}

func TestUploadFileLocal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "invoice.pdf"), []byte("%PDF-1.4 test"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cs := connect(t, uploadHandler(t, &calls, "Рахунок.pdf", "%PDF-1.4 test"), keycrm.UploadConfig{UploadDir: dir})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "upload_file", map[string]any{"file_path": "invoice.pdf", "filename": "Рахунок.pdf"}))
	if out["id"] != float64(330) || calls.Load() != 1 {
		t.Errorf("результат: %v, запитів=%d", out, calls.Load())
	}

	res := server.CallTool(t, cs, "upload_file", map[string]any{"file_path": "../outside.pdf"})
	if !res.IsError || calls.Load() != 1 {
		t.Errorf("файл поза KEYCRM_UPLOAD_DIR: IsError=%v, %s", res.IsError, server.ResultText(res))
	}
}

func TestUploadFileFromURL(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "image-bytes")
	}))
	t.Cleanup(src.Close)

	var calls atomic.Int32
	// Тестове джерело — на 127.0.0.1, тож потрібен AllowPrivate.
	cs := connect(t, uploadHandler(t, &calls, "photo.jpg", "image-bytes"), keycrm.UploadConfig{AllowPrivate: true})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "upload_file", map[string]any{"url": src.URL + "/img/photo.jpg"}))
	if out["id"] != float64(330) || calls.Load() != 1 {
		t.Errorf("результат: %v, запитів=%d", out, calls.Load())
	}
}

func TestUploadFileBlocksPrivateURL(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secret")
	}))
	t.Cleanup(src.Close)

	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, keycrm.UploadConfig{})
	res := server.CallTool(t, cs, "upload_file", map[string]any{"url": src.URL + "/x"})
	if !res.IsError || !strings.Contains(server.ResultText(res), "private address") || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d: %s", res.IsError, calls.Load(), server.ResultText(res))
	}
}

func TestListFilesSortAndPaging(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/storage" || q.Get("sort") != "-size" || q.Get("limit") != "10" || q.Get("page") != "1" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":1,"current_page":1,"data":[{"id":330,"size":563263}],"next_page_url":null}`)
	}, keycrm.UploadConfig{})
	out := server.Decode[listing.Result[map[string]any]](t, server.CallTool(t, cs, "list_files", map[string]any{"sort": "-size", "limit": 10}))
	if out.HasNext || len(out.Items) != 1 {
		t.Errorf("результат: %+v", out)
	}
}

func TestListEntityFilesEntityTypeEnum(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/storage/attachment/tasks/4090" {
			t.Errorf("шлях %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"id":1},{"id":2}]`)
	}, keycrm.UploadConfig{})

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var enum []any
	for _, tool := range tools.Tools {
		if tool.Name == "list_entity_files" {
			schema, _ := tool.InputSchema.(map[string]any)
			props, _ := schema["properties"].(map[string]any)
			et, _ := props["entity_type"].(map[string]any)
			enum, _ = et["enum"].([]any)
		}
	}
	if !slices.Equal(enum, []any{"order", "pipelines", "tasks"}) {
		t.Errorf("enum entity_type = %v, у api.yaml: order, pipelines, tasks", enum)
	}

	res := server.CallTool(t, cs, "list_entity_files", map[string]any{"entity_type": "client", "entity_id": 1})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("невідомий тип: IsError=%v, запитів=%d", res.IsError, calls.Load())
	}

	out := server.Decode[EntityFilesResult](t, server.CallTool(t, cs, "list_entity_files", map[string]any{"entity_type": "tasks", "entity_id": 4090}))
	if len(out.Items) != 2 || calls.Load() != 1 {
		t.Errorf("результат: %+v", out)
	}
}
