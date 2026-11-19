// SPDX-License-Identifier: AGPL-3.0-only

package calls

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

// connect підключає клієнта MCP до групи calls поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

func TestListCallsFiltersAndProjection(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/calls" || q.Get("filter[type]") != "incoming" || q.Get("filter[client_id]") != "4545" ||
			q.Get("filter[client_phone]") != "+380671234567" || q.Get("limit") != "10" || q.Get("page") != "1" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if q.Get("include") != "client,manager" {
			t.Errorf("include = %q", q.Get("include"))
		}
		_, _ = io.WriteString(w, `{"total":1,"current_page":1,"next_page_url":null,"data":[
			{"id":7,"type":"incoming","state":"completed","external_number":"+380671234567","internal_number":"101","duration":42,
			 "user_id":12,"client_id":4545,"lead_id":null,"comment":null,"call_url":"https://example.com/r.mp3","utm_source":"fb","created_at":"2026-11-19 09:00:00",
			 "client":{"id":4545,"full_name":"Іван Петренко"},"manager":{"id":12,"full_name":"Олена"}}]}`)
	})
	out := server.Decode[listing.Result[CallSummary]](t, server.CallTool(t, cs, "list_calls", map[string]any{
		"type": "incoming", "client_id": []int{4545}, "client_phone": "067 123 45 67", "limit": 10,
	}))
	want := CallSummary{
		ID: 7, Type: "incoming", State: "completed", ExternalNumber: "+380671234567", InternalNumber: "101", Duration: 42,
		ClientID: 4545, ClientName: "Іван Петренко", ManagerID: 12, ManagerName: "Олена",
		CallURL: "https://example.com/r.mp3", CreatedAt: "2026-11-19 09:00:00",
	}
	if len(out.Items) != 1 || out.Items[0] != want {
		t.Errorf("проєкція: %+v", out.Items)
	}
}

func TestListCallsRejectsBadInput(t *testing.T) {
	tests := map[string]map[string]any{
		"невідомий тип":   {"type": "missed"},
		"поганий телефон": {"client_phone": "123"},
		"невідоме поле":   {"include": []string{"lead"}},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			res := server.CallTool(t, cs, "list_calls", args)
			if !res.IsError || calls.Load() != 0 {
				t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
			}
		})
	}
}
