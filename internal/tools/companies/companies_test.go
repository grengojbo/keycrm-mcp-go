// SPDX-License-Identifier: AGPL-3.0-only

package companies

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

// connect підключає клієнта MCP до групи companies поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

func TestListCompaniesFiltersAndPaging(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/companies" || q.Get("filter[company_id]") != "112,113" || q.Get("limit") != "50" || q.Get("page") != "3" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":2,"current_page":3,"next_page_url":null,"data":[
			{"id":112,"name":"Приклад","title":"ТОВ «Приклад»","bank_account":"UA39","manager_id":12,"created_at":"2026-11-18 10:00:00"}]}`)
	})
	out := server.Decode[listing.Result[CompanySummary]](t, server.CallTool(t, cs, "list_companies", map[string]any{
		"company_id": []int{112, 113}, "limit": 51, "page": 3,
	}))
	want := CompanySummary{ID: 112, Name: "Приклад", Title: "ТОВ «Приклад»", ManagerID: 12, CreatedAt: "2026-11-18 10:00:00"}
	if out.Note != "limit capped at 50" || out.HasNext || len(out.Items) != 1 || out.Items[0] != want {
		t.Errorf("сторінка: %+v", out)
	}
}

func TestGetCompanyDefaultInclude(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/companies/112" || r.URL.Query().Get("include") != "manager,buyer,custom_fields" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"id":112,"name":"Приклад"}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "get_company", map[string]any{"company_id": 112}))
	if out["name"] != "Приклад" {
		t.Errorf("результат: %v", out)
	}
}

func TestGetCompanyNotFound(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not found"}`)
	})
	if res := server.CallTool(t, cs, "get_company", map[string]any{"company_id": 1}); !res.IsError {
		t.Errorf("очікувалась помилка: %s", server.ResultText(res))
	}
}

func TestCreateCompanySendsBody(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/companies" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"name": "Приклад", "title": "ТОВ «Приклад»", "buyer_id": []any{"4545", "4546"}}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":114,"name":"Приклад"}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_company", map[string]any{
		"name": "Приклад", "title": "ТОВ «Приклад»", "buyer_id": []int{4545, 4546},
	}))
	if out["id"] != float64(114) {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateCompanyNeedsName(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "create_company", map[string]any{"name": "  "})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
	}
}

func TestUpdateCompany(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/companies/112" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body, map[string]any{"notes": "Оплата за рахунком"}) {
			t.Errorf("тіло: %v", body)
		}
		_, _ = io.WriteString(w, `{"id":112}`)
	})
	if res := server.CallTool(t, cs, "update_company", map[string]any{"company_id": 112, "notes": "Оплата за рахунком"}); res.IsError {
		t.Error(server.ResultText(res))
	}
}

func TestUpdateCompanyNeedsChanges(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "update_company", map[string]any{"company_id": 112})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
	}
}
