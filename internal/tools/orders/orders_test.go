// SPDX-License-Identifier: AGPL-3.0-only

package orders

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

// connect підключає клієнта MCP до групи orders поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

func TestListOrdersFiltersAndPaging(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/order" || q.Get("filter[status_id]") != "3" || q.Get("limit") != "50" || q.Get("page") != "2" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if q.Get("filter[payment_status]") != "paid,overpaid" || q.Get("filter[buyer_phone]") != "+380671234567" {
			t.Errorf("фільтри: %s", r.URL.RawQuery)
		}
		if q.Get("include") != "buyer,status" {
			t.Errorf("include = %q", q.Get("include"))
		}
		_, _ = io.WriteString(w, `{"total":51,"current_page":2,"data":[
			{"id":101,"source_uuid":555,"status_id":3,"grand_total":"1300.50","payment_status":"paid","created_at":"2026-11-18T10:00:00.000000Z",
			 "status":{"name":"Новий"},"buyer":{"full_name":"Іван Петренко","phone":"+380671234567"}}],
			"next_page_url":"https://openapi.keycrm.app/v1/order?page=3"}`)
	})
	res := server.CallTool(t, cs, "list_orders", map[string]any{
		"status_id": 3, "limit": 100, "page": 2,
		"payment_status": []string{"paid", "overpaid"}, "buyer_phone": "+380671234567",
	})
	out := server.Decode[listing.Result[OrderSummary]](t, res)
	if out.Note != "limit capped at 50" || !out.HasNext || out.Page != 2 || out.Total != 51 {
		t.Errorf("сторінка: %+v", out)
	}
	want := OrderSummary{ID: 101, SourceUUID: "555", StatusID: 3, Status: "Новий", PaymentStatus: "paid", GrandTotal: 1300.5, BuyerName: "Іван Петренко", CreatedAt: "2026-11-18T10:00:00.000000Z"}
	if len(out.Items) != 1 || out.Items[0] != want {
		t.Errorf("проєкція: %+v", out.Items)
	}
	if strings.Contains(server.ResultText(res), "+380671234567") {
		t.Error("проєкція не має містити телефон")
	}
}

func TestListOrdersRejectsUnknownPaymentStatus(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "list_orders", map[string]any{"payment_status": []string{"free"}})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
	}
}

func TestGetOrderShowsShipmentType(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/order/1024" {
			t.Errorf("шлях %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("include"); got != strings.Join(defaultInclude, ",") {
			t.Errorf("include = %q", got)
		}
		_, _ = io.WriteString(w, `{"id":1024,"products":[
			{"sku":"PLA-1","quantity":0.75,"shipment_type":"manufacturing","picture":{"thumbnail":"https://cdn.example.com/t.jpg"}},
			{"sku":"PETG-2","quantity":1,"shipment_type":"warehouse","warehouse":{"id":3,"name":"Київ №1"}}]}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "get_order", map[string]any{"order_id": 1024}))
	products, _ := out["products"].([]any)
	if len(products) != 2 {
		t.Fatalf("products: %v", out)
	}
	first := products[0].(map[string]any)
	if first["shipment_type"] != "manufacturing" || first["quantity"] != 0.75 {
		t.Errorf("перший товар: %v", first)
	}
	// У реальному keyCRM picture — об'єкт, хоча api.yaml каже «рядок»:
	// інструмент не має падати на розбіжностях зі специфікацією.
	if pic, ok := first["picture"].(map[string]any); !ok || pic["thumbnail"] == nil {
		t.Errorf("picture: %v", first["picture"])
	}
	if products[1].(map[string]any)["shipment_type"] != "warehouse" {
		t.Errorf("другий товар: %v", products[1])
	}
}

func TestCreateOrderNeedsConfirm(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "create_order", map[string]any{
		"source_id": 1, "buyer": map[string]any{"full_name": "Іван"},
	})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") {
		t.Errorf("очікувалась вимога confirm: %s", server.ResultText(res))
	}
	if calls.Load() != 0 {
		t.Errorf("без confirm зроблено %d запитів", calls.Load())
	}
}

func TestCreateOrderSendsBody(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/order" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["confirm"]; ok {
			t.Error("confirm не має йти в keyCRM")
		}
		products := body["products"].([]any)
		if body["source_id"] != float64(1) || products[0].(map[string]any)["quantity"] != 0.75 {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":2048,"source_id":1}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_order", map[string]any{
		"source_id": 1, "buyer": map[string]any{"full_name": "Іван", "phone": "+380671234567"},
		"products": []any{map[string]any{"sku": "PLA-1", "price": 650, "quantity": 0.75}},
		"confirm":  true,
	}))
	if out["id"] != float64(2048) {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateOrderValidatesPaymentMethod(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "create_order", map[string]any{
		"source_id": 1, "buyer": map[string]any{}, "confirm": true,
		"payments": []any{map[string]any{"amount": 100}},
	})
	if !res.IsError || !strings.Contains(server.ResultText(res), "payments[0]") || calls.Load() != 0 {
		t.Errorf("IsError=%v текст=%q запитів=%d", res.IsError, server.ResultText(res), calls.Load())
	}
}

func TestImportOrdersNeedsConfirmAndLimit(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
	})
	order := map[string]any{"source_id": 1, "buyer": map[string]any{"full_name": "Іван"}}
	if res := server.CallTool(t, cs, "import_orders", map[string]any{"orders": []any{order}}); !res.IsError {
		t.Error("без confirm має бути IsError")
	}
	many := make([]any, maxImport+1)
	for i := range many {
		many[i] = order
	}
	if res := server.CallTool(t, cs, "import_orders", map[string]any{"orders": many, "confirm": true}); !res.IsError {
		t.Error("понад 50 замовлень має бути IsError")
	}
	if calls.Load() != 0 {
		t.Fatalf("зроблено %d запитів до валідації", calls.Load())
	}
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "import_orders", map[string]any{"orders": []any{order}, "confirm": true}))
	if out["submitted"] != float64(1) || out["ok"] != true {
		t.Errorf("результат: %v", out)
	}
}

func TestOrderErrorFieldsBecomeIsError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"status_id":["The selected status id is invalid."]}}`)
	})
	res := server.CallTool(t, cs, "update_order", map[string]any{"order_id": 1, "status_id": 999})
	if !res.IsError || !strings.Contains(server.ResultText(res), "status_id: The selected status id is invalid.") {
		t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
	}
}

func TestUpdateOrderNeedsFields(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	if res := server.CallTool(t, cs, "update_order", map[string]any{"order_id": 1}); !res.IsError || calls.Load() != 0 {
		t.Errorf("порожнє оновлення: IsError=%v запитів=%d", res.IsError, calls.Load())
	}
}

func TestPathParams(t *testing.T) {
	cases := []struct {
		tool         string
		args         map[string]any
		method, path string
	}{
		{"add_order_tag", map[string]any{"order_id": 7, "tag_id": 4}, http.MethodPost, "/order/7/tag/4"},
		{"remove_order_tag", map[string]any{"order_id": 7, "tag_id": 4}, http.MethodDelete, "/order/7/tag/4"},
		{"attach_order_file", map[string]any{"order_id": 7, "file_id": 9}, http.MethodPost, "/order/7/attachment/9"},
		{"detach_order_file", map[string]any{"order_id": 7, "file_id": 9}, http.MethodDelete, "/order/7/attachment/9"},
		{"add_order_payment", map[string]any{"order_id": 7, "payment_method_id": 2, "amount": 10}, http.MethodPost, "/order/7/payment"},
		{"update_order_payment", map[string]any{"order_id": 7, "payment_id": 3, "status": "canceled"}, http.MethodPut, "/order/7/payment/3"},
		{"add_order_expense", map[string]any{"order_id": 7, "expense_type": "Пакування", "amount": 5}, http.MethodPost, "/order/7/expense"},
		{"update_order_expense", map[string]any{"order_id": 7, "expense_id": 5, "status": "canceled"}, http.MethodPut, "/order/7/expense/5"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("%s %s, очікувано %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			if res := server.CallTool(t, cs, tc.tool, tc.args); res.IsError {
				t.Errorf("IsError: %s", server.ResultText(res))
			}
		})
	}
}

func TestReadOnlyOrders(t *testing.T) {
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{}, true)
	var names []string
	for tool, err := range cs.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "get_order,list_orders" && strings.Join(names, ",") != "list_orders,get_order" {
		t.Errorf("read-only: %v", names)
	}
}
