// SPDX-License-Identifier: AGPL-3.0-only

package pipelines

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/orders"
)

// connect підключає клієнта MCP до групи pipelines поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

// toolNames повертає назви інструментів, які бачить клієнт.
func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	var names []string
	for tool, err := range cs.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestPipelinesDisabledByToolsetsEnv(t *testing.T) {
	env := map[string]string{"KEYCRM_API_KEY": "test", "KEYCRM_TOOLSETS": "orders"}
	cfg, err := config.Load(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	srv, _, err := server.New(cfg, server.Deps{}, []server.Toolset{orders.Toolset, Toolset})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	names := toolNames(t, cs)
	if !slices.Contains(names, "list_orders") {
		t.Fatalf("група orders не ввімкнулась: %v", names)
	}
	for _, n := range names {
		if strings.Contains(n, "pipeline") || strings.Contains(n, "card") {
			t.Errorf("інструмент воронок %q видно при KEYCRM_TOOLSETS=orders", n)
		}
	}
}

func TestListCardsFilters(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/pipelines/cards" || q.Get("limit") != "50" || q.Get("page") != "2" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if q.Get("filter[pipeline_id]") != "1" || q.Get("filter[status_id]") != "4,5" || q.Get("filter[source_id]") != "2" ||
			q.Get("filter[created_between]") != "2026-11-01 00:00:00,2026-11-30 23:59:59" {
			t.Errorf("фільтри: %s", r.URL.RawQuery)
		}
		if q.Get("include") != strings.Join(listInclude, ",") {
			t.Errorf("include = %q", q.Get("include"))
		}
		_, _ = io.WriteString(w, `{"total":60,"current_page":2,"data":[
			{"id":31,"title":"Котушка PETG під замовлення","status_id":4,"source_id":2,"manager_id":7,"contact_id":12,
			 "communicate_at":"2026-11-20 10:00:00","created_at":"2026-11-18T10:00:00.000000Z","manager_comment":"довгий коментар",
			 "status":{"id":4,"pipeline_id":1,"title":"Переговори"},
			 "contact":{"id":12,"full_name":"Олена Коваль","phone":"+380671234567","client_id":88}}],
			"next_page_url":"https://openapi.keycrm.app/v1/pipelines/cards?page=3"}`)
	})
	res := server.CallTool(t, cs, "list_pipeline_cards", map[string]any{
		"pipeline_id": 1, "status_id": []int{4, 5}, "source_id": 2,
		"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59", "limit": 100, "page": 2,
	})
	out := server.Decode[listing.Result[CardSummary]](t, res)
	if out.Note != "limit capped at 50" || !out.HasNext || out.Page != 2 || out.Total != 60 {
		t.Errorf("сторінка: %+v", out)
	}
	want := CardSummary{
		ID: 31, Title: "Котушка PETG під замовлення", PipelineID: 1, StatusID: 4, Status: "Переговори",
		SourceID: 2, ManagerID: 7, ContactName: "Олена Коваль", ClientID: 88,
		CommunicateAt: "2026-11-20 10:00:00", CreatedAt: "2026-11-18T10:00:00.000000Z",
	}
	if len(out.Items) != 1 || out.Items[0] != want {
		t.Errorf("проєкція: %+v", out.Items)
	}
	if text := server.ResultText(res); strings.Contains(text, "+380671234567") || strings.Contains(text, "довгий коментар") {
		t.Error("проєкція не має містити телефон і коментар менеджера")
	}
}

func TestListCardsRejectsUnknownInclude(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "get_pipeline_card", map[string]any{"card_id": 1, "include": []string{"buyer"}})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
	}
}

func TestListPipelineStatuses(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pipelines/3/statuses" || r.URL.Query().Get("limit") != "25" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":1,"current_page":1,"data":[
			{"id":4,"pipeline_id":3,"title":"Переговори","alias":"talks","is_final":false,"created_at":"2026-01-01"}],"next_page_url":null}`)
	})
	out := server.Decode[listing.Result[map[string]any]](t, server.CallTool(t, cs, "list_pipeline_statuses", map[string]any{"pipeline_id": 3}))
	if len(out.Items) != 1 || out.Items[0]["title"] != "Переговори" || out.HasNext {
		t.Fatalf("результат: %+v", out)
	}
	if _, ok := out.Items[0]["created_at"]; ok {
		t.Error("службові дати мають бути прибрані")
	}
}

func TestListPipelineStatusesNeedsPipeline(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	if res := server.CallTool(t, cs, "list_pipeline_statuses", map[string]any{}); !res.IsError {
		t.Error("без pipeline_id має бути IsError")
	}
	res := server.CallTool(t, cs, "list_pipeline_statuses", map[string]any{"pipeline_id": 0})
	if !res.IsError || !strings.Contains(server.ResultText(res), "list_pipelines") || calls.Load() != 0 {
		t.Errorf("IsError=%v текст=%q запитів=%d", res.IsError, server.ResultText(res), calls.Load())
	}
}

func TestGetPipelineCardDefaultInclude(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pipelines/cards/31" {
			t.Errorf("шлях %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("include"); got != strings.Join(defaultInclude, ",") {
			t.Errorf("include = %q", got)
		}
		_, _ = io.WriteString(w, `{"id":31,"products":[{"sku":"PETG-1","quantity":0.75}]}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "get_pipeline_card", map[string]any{"card_id": 31}))
	if out["id"] != float64(31) {
		t.Errorf("результат: %v", out)
	}
}

func TestUpdateCardNoteAndStatus(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/pipelines/cards/31" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["note"] != "Клієнт просить чорний колір" || body["status_id"] != float64(5) || len(body) != 2 {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":true}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "update_pipeline_card", map[string]any{
		"card_id": 31, "note": "Клієнт просить чорний колір", "status_id": 5,
	}))
	if out["status"] != true {
		t.Errorf("результат: %v", out)
	}
}

func TestUpdateCardNeedsFields(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	if res := server.CallTool(t, cs, "update_pipeline_card", map[string]any{"card_id": 31}); !res.IsError || calls.Load() != 0 {
		t.Errorf("порожнє оновлення: IsError=%v запитів=%d", res.IsError, calls.Load())
	}
}

func TestCreateCardSendsContactAndPayments(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pipelines/cards" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		contact, _ := body["contact"].(map[string]any)
		products, _ := body["products"].([]any)
		if contact["client_id"] != float64(88) || body["pipeline_id"] != float64(1) || len(products) != 1 ||
			products[0].(map[string]any)["quantity"] != 0.75 {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":32,"contact_id":12}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_pipeline_card", map[string]any{
		"pipeline_id": 1, "contact": map[string]any{"client_id": 88},
		"products": []any{map[string]any{"sku": "PETG-1", "price": 650, "quantity": 0.75}},
		"payments": []any{map[string]any{"payment_method_id": 2, "amount": 300}},
	}))
	if out["id"] != float64(32) {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateCardValidation(t *testing.T) {
	cases := map[string]map[string]any{
		"без контакту":         {"title": "Лід"},
		"оплата без способу":   {"contact": map[string]any{"full_name": "Олена"}, "payments": []any{map[string]any{"amount": 10}}},
		"порожній контакт":     {"contact": map[string]any{}},
		"оплата в add_payment": nil,
	}
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	for name, args := range cases {
		tool := "create_pipeline_card"
		if args == nil {
			tool, args = "add_card_payment", map[string]any{"card_id": 31, "amount": 10}
		}
		if res := server.CallTool(t, cs, tool, args); !res.IsError {
			t.Errorf("%s: очікувалась помилка валідації", name)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("до keyCRM пішло %d запитів", calls.Load())
	}
}

func TestCardErrorFieldsBecomeIsError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"status_id":["The selected status id is invalid."]}}`)
	})
	res := server.CallTool(t, cs, "update_pipeline_card", map[string]any{"card_id": 1, "status_id": 999})
	if !res.IsError || !strings.Contains(server.ResultText(res), "status_id: The selected status id is invalid.") {
		t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
	}
}

func TestPipelinesDisabledInCRMBecomesIsError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"Leads are not enabled"}`)
	})
	res := server.CallTool(t, cs, "list_pipelines", map[string]any{})
	if !res.IsError || !strings.Contains(server.ResultText(res), "Leads are not enabled") {
		t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
	}
}

func TestPathParams(t *testing.T) {
	cases := []struct {
		tool         string
		args         map[string]any
		method, path string
	}{
		{"list_pipelines", map[string]any{}, http.MethodGet, "/pipelines"},
		{"add_card_payment", map[string]any{"card_id": 7, "payment_method_id": 2, "amount": 10}, http.MethodPost, "/pipelines/cards/7/payment"},
		{"update_card_payment", map[string]any{"card_id": 7, "payment_id": 3, "status": "canceled"}, http.MethodPut, "/pipelines/cards/7/payment/3"},
		{"attach_card_file", map[string]any{"card_id": 7, "file_id": 9}, http.MethodPost, "/pipelines/cards/7/attachment/9"},
		{"detach_card_file", map[string]any{"card_id": 7, "file_id": 9}, http.MethodDelete, "/pipelines/cards/7/attachment/9"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("%s %s, очікувано %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"data":[]}`)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			if res := server.CallTool(t, cs, tc.tool, tc.args); res.IsError {
				t.Errorf("IsError: %s", server.ResultText(res))
			}
		})
	}
}

func TestReadOnlyPipelines(t *testing.T) {
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{}, true)
	got := strings.Join(toolNames(t, cs), ",")
	if got != "get_pipeline_card,list_pipeline_cards,list_pipeline_statuses,list_pipelines" {
		t.Errorf("read-only: %s", got)
	}
}
