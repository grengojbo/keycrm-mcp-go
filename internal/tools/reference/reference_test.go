// SPDX-License-Identifier: AGPL-3.0-only

package reference

import (
	"io"
	"net/http"
	"testing"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

func TestSimpleListsPathsAndPaging(t *testing.T) {
	paths := map[string]string{
		"list_order_statuses":    "/order/status",
		"list_order_sources":     "/order/source",
		"list_order_tags":        "/order/tag",
		"list_payment_methods":   "/order/payment-method",
		"list_expense_types":     "/order/expense-type",
		"list_delivery_services": "/order/delivery-service",
		"list_product_statuses":  "/order/product-status",
	}
	for tool, path := range paths {
		t.Run(tool, func(t *testing.T) {
			h := func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path || r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("page") != "1" {
					t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
				}
				_, _ = io.WriteString(w, `{"total":1,"current_page":1,"data":[{"id":1,"name":"Новий","alias":"new","group_id":1,"is_closing_order":false,"expiration_period":null,"created_at":"2026-01-01T00:00:00.000000Z","updated_at":"2026-01-01T00:00:00.000000Z","deleted_at":null}],"next_page_url":null}`)
			}
			cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.HandlerFunc(h))}, true)
			out := server.Decode[listing.Result[Item]](t, server.CallTool(t, cs, tool, map[string]any{}))
			if len(out.Items) != 1 || out.HasNext {
				t.Fatalf("результат: %+v", out)
			}
			want := map[string]any{"id": float64(1), "name": "Новий", "alias": "new", "group_id": float64(1), "is_closing_order": false}
			if len(out.Items[0]) != len(want) {
				t.Errorf("службові поля не прибрано: %v", out.Items[0])
			}
			for k, v := range want {
				if out.Items[0][k] != v {
					t.Errorf("%s = %v, очікувано %v", k, out.Items[0][k], v)
				}
			}
		})
	}
}

func TestListUsersFilters(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/users" || q.Get("filter[status]") != "active" || q.Get("filter[role_id]") != "1,2" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.HandlerFunc(h))}, true)
	server.Decode[listing.Result[Item]](t, server.CallTool(t, cs, "list_users", map[string]any{"status": []string{"active"}, "role_id": []int{1, 2}}))

	if res := server.CallTool(t, cs, "list_users", map[string]any{"status": []string{"fired"}}); !res.IsError {
		t.Error("статус поза enum має давати IsError")
	}
}

func TestListCustomFields(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/custom-fields" || q.Get("filter[model]") != "order,client" || q.Get("include") != "options" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[{"uuid":"OR_1037","name":"Розмір","model":"order"}]`)
	}
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.HandlerFunc(h))}, true)
	out := server.Decode[CustomFieldsResult](t, server.CallTool(t, cs, "list_custom_fields", map[string]any{
		"model": []string{"order", "client"}, "include_options": true,
	}))
	if len(out.Items) != 1 || out.Items[0]["uuid"] != "OR_1037" {
		t.Errorf("результат: %+v", out)
	}
}

func TestReferenceErrorIsError(t *testing.T) {
	h := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.HandlerFunc(h))}, true)
	res := server.CallTool(t, cs, "list_order_statuses", nil)
	if !res.IsError || server.ResultText(res) == "" {
		t.Errorf("401: IsError=%v %q", res.IsError, server.ResultText(res))
	}
}
