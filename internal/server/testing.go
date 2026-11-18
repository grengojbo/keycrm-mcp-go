// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// Хелпери для тестів toolsets: сервер з групами sets, підключений до клієнта
// MCP через in-memory транспорт SDK, і фейковий keyCRM на httptest.

// Connect створює сервер з усіма групами sets і повертає підключену клієнтську
// сесію. Сесії закриваються після тесту.
func Connect(t testing.TB, sets []Toolset, d Deps, readOnly bool) *mcp.ClientSession {
	t.Helper()
	srv, _, err := New(config.Config{Toolsets: []string{"all"}, ReadOnly: readOnly}, d, sets)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	// Не t.Context(): він скасовується до Cleanup, а сесії мають жити до кінця тесту.
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	return cs
}

// CallTool викликає інструмент і падає лише на помилці протоколу; помилка
// інструмента повертається як результат з IsError.
func CallTool(t testing.TB, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

// ResultText склеює текстові блоки результату.
func ResultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Decode розбирає структурований результат інструмента в T; падає, якщо
// інструмент повернув помилку.
func Decode[T any](t testing.TB, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	if res.IsError {
		t.Fatalf("інструмент повернув помилку: %s", ResultText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode structured content: %v; %s", err, b)
	}
	return out
}

// FakeKeyCRM запускає httptest-сервер з обробником h і повертає клієнт keyCRM,
// спрямований на нього, без обмеження частоти запитів.
func FakeKeyCRM(t testing.TB, h http.Handler) *keycrm.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := keycrm.New(keycrm.Config{APIKey: "test", BaseURL: srv.URL, Limiter: rate.NewLimiter(rate.Inf, 1)})
	if err != nil {
		t.Fatalf("keycrm.New: %v", err)
	}
	return c
}
