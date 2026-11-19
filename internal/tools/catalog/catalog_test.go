// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

// connect підключає клієнта MCP до групи catalog поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

// decodeBody розбирає JSON-тіло запиту до фейкового keyCRM.
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("тіло запиту: %v", err)
	}
	return body
}

func TestUpdateOffersBySKUOrID(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/offers" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		offers := decodeBody(t, r)["offers"].([]any)
		first, second := offers[0].(map[string]any), offers[1].(map[string]any)
		if first["sku"] != "PLA-1" || first["id"] != nil || second["id"] != float64(22) || second["sku"] != nil {
			t.Errorf("позиції: %v", offers)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":true}`)
	})
	price := 690.0
	bad := []struct {
		name  string
		offer map[string]any
		want  string
	}{
		{"без id і sku", map[string]any{"price": price}, "exactly one of id or sku"},
		{"і id, і sku", map[string]any{"id": 22, "sku": "PLA-1", "price": price}, "exactly one of id or sku"},
		{"нічого не змінює", map[string]any{"sku": "PLA-1"}, "nothing to update"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			res := server.CallTool(t, cs, "update_offers", map[string]any{"offers": []any{tc.offer}})
			if !res.IsError || !strings.Contains(server.ResultText(res), "offers[0]: ") || !strings.Contains(server.ResultText(res), tc.want) {
				t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("до валідації зроблено %d запитів", calls.Load())
	}
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "update_offers", map[string]any{"offers": []any{
		map[string]any{"sku": "PLA-1", "price": price},
		map[string]any{"id": 22, "purchased_price": 430},
	}}))
	if out["submitted"] != float64(2) || out["status"] != true || calls.Load() != 1 {
		t.Errorf("результат: %v, запитів %d", out, calls.Load())
	}
}

func TestUpdateOffersConfirmAbove20(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":true}`)
	})
	offers := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"sku": fmt.Sprintf("SKU-%d", i), "price": 100}
		}
		return out
	}
	res := server.CallTool(t, cs, "update_offers", map[string]any{"offers": offers(21)})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") || calls.Load() != 0 {
		t.Fatalf("21 позиція без confirm: IsError=%v текст=%q запитів=%d", res.IsError, server.ResultText(res), calls.Load())
	}
	if res := server.CallTool(t, cs, "update_offers", map[string]any{"offers": offers(20)}); res.IsError {
		t.Errorf("20 позицій без confirm: %s", server.ResultText(res))
	}
	if res := server.CallTool(t, cs, "update_offers", map[string]any{"offers": offers(21), "confirm": true}); res.IsError {
		t.Errorf("21 позиція з confirm: %s", server.ResultText(res))
	}
	if calls.Load() != 2 {
		t.Errorf("запитів %d, очікувалось 2", calls.Load())
	}
}

func TestUpdateOffersSendsPurchasedPrice(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if _, ok := body["confirm"]; ok {
			t.Error("confirm не має йти в keyCRM")
		}
		offer := body["offers"].([]any)[0].(map[string]any)
		if offer["purchased_price"] != 53.5 || offer["sku"] != "PLA-1" {
			t.Errorf("позиція: %v", offer)
		}
		if _, ok := offer["price"]; ok {
			t.Errorf("price не передавали, а він є: %v", offer)
		}
		// Нульова собівартість — допустиме значення, вона не має губитися.
		zero := body["offers"].([]any)[1].(map[string]any)
		if v, ok := zero["purchased_price"]; !ok || v != float64(0) {
			t.Errorf("нульова собівартість загубилась: %v", zero)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	res := server.CallTool(t, cs, "update_offers", map[string]any{"offers": []any{
		map[string]any{"sku": "PLA-1", "purchased_price": 53.5},
		map[string]any{"id": 7, "purchased_price": 0},
	}})
	if res.IsError {
		t.Fatal(server.ResultText(res))
	}
}

func TestListOffersFilterSKUs(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/offers" || q.Get("filter[sku]") != "PLA-1,PETG-2" || q.Get("include") != "product" || q.Get("limit") != "25" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":1,"current_page":1,"next_page_url":null,"data":[
			{"id":22,"product_id":101,"sku":"PLA-1","price":"650.00","purchased_price":420.5,"quantity":7,"in_reserve":1,
			 "properties":[{"name":"Колір","value":"Чорний"}],"product":{"name":"PLA пластик","description":"довгий опис"}}]}`)
	})
	out := server.Decode[listing.Result[OfferSummary]](t, server.CallTool(t, cs, "list_offers", map[string]any{"skus": []string{"PLA-1", "PETG-2"}}))
	if len(out.Items) != 1 || out.HasNext {
		t.Fatalf("сторінка: %+v", out)
	}
	got := out.Items[0]
	if got.ID != 22 || got.ProductName != "PLA пластик" || got.Price != 650 || got.PurchasedPrice != 420.5 || got.Quantity != 7 || got.InReserve != 1 || len(got.Properties) != 1 {
		t.Errorf("проєкція: %+v", got)
	}
}

func TestListOffersRejectsSKUWithComma(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "list_offers", map[string]any{"skus": []string{"A,B"}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "SKU must not contain commas") || calls.Load() != 0 {
		t.Errorf("IsError=%v текст=%q запитів=%d", res.IsError, server.ResultText(res), calls.Load())
	}
}

func TestListProductsFiltersAndProjection(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/products" || q.Get("filter[category_id]") != "4,5" || q.Get("filter[is_archived]") != "false" || q.Get("limit") != "50" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":60,"current_page":1,"next_page_url":"https://openapi.keycrm.app/v1/products?page=2","data":[
			{"id":101,"name":"PLA пластик","sku":"PLA","quantity":12.5,"min_price":650,"max_price":"700.00","unit_type":null,
			 "category_id":4,"has_offers":true,"is_archived":false,"description":"довгий опис"}]}`)
	})
	res := server.CallTool(t, cs, "list_products", map[string]any{"category_ids": []int{4, 5}, "is_archived": false, "limit": 70})
	out := server.Decode[listing.Result[ProductSummary]](t, res)
	want := ProductSummary{ID: 101, Name: "PLA пластик", SKU: "PLA", Quantity: 12.5, MinPrice: 650, MaxPrice: 700, CategoryID: 4, HasOffers: true}
	if len(out.Items) != 1 || out.Items[0] != want || !out.HasNext || out.Note != "limit capped at 50" {
		t.Errorf("результат: %+v", out)
	}
	if strings.Contains(server.ResultText(res), "довгий опис") {
		t.Error("проєкція не має містити опис")
	}
}

func TestImportProductsNeedsConfirm(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/products/import" {
			t.Errorf("шлях %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"status":true}`)
	})
	product := map[string]any{"name": "PLA", "sku": "PLA-1"}
	if res := server.CallTool(t, cs, "import_products", map[string]any{"products": []any{product}}); !res.IsError {
		t.Error("без confirm має бути IsError")
	}
	many := make([]any, maxImport+1)
	for i := range many {
		many[i] = product
	}
	if res := server.CallTool(t, cs, "import_products", map[string]any{"products": many, "confirm": true}); !res.IsError {
		t.Error("понад 100 товарів має бути IsError")
	}
	noName := map[string]any{"name": " "}
	if res := server.CallTool(t, cs, "import_products", map[string]any{"products": []any{noName}, "confirm": true}); !res.IsError ||
		!strings.Contains(server.ResultText(res), "products[0].name") {
		t.Errorf("товар без назви: %s", server.ResultText(res))
	}
	if calls.Load() != 0 {
		t.Fatalf("до валідації зроблено %d запитів", calls.Load())
	}
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "import_products", map[string]any{"products": []any{product}, "confirm": true}))
	if out["submitted"] != float64(1) || out["status"] != true {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateProductAndOffers(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		switch r.URL.Path {
		case "/products":
			if body["name"] != "PLA" || body["purchased_price"] != float64(420) || body["category_id"] != float64(4) {
				t.Errorf("товар: %v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":101,"name":"PLA"}`)
		case "/products/101/offers":
			offer := body["offers"].([]any)[0].(map[string]any)
			if offer["sku"] != "PLA-RED" || offer["purchased_price"] != float64(410) {
				t.Errorf("варіант: %v", offer)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"status":true}`)
		default:
			t.Errorf("шлях %s", r.URL.Path)
		}
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_product", map[string]any{
		"name": "PLA", "sku": "PLA-1", "price": 650, "purchased_price": 420, "category_id": 4,
	}))
	if out["id"] != float64(101) {
		t.Errorf("товар: %v", out)
	}
	out = server.Decode[map[string]any](t, server.CallTool(t, cs, "create_offers", map[string]any{
		"product_id": 101,
		"offers": []any{map[string]any{"sku": "PLA-RED", "price": 650, "purchased_price": 410,
			"properties": []any{map[string]any{"name": "Колір", "value": "Червоний"}}}},
	}))
	if out["submitted"] != float64(1) {
		t.Errorf("варіанти: %v", out)
	}
}

func TestUpdateProductNeedsFields(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/products/101" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if body := decodeBody(t, r); body["price"] != float64(0) {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"id":101}`)
	})
	if res := server.CallTool(t, cs, "update_product", map[string]any{"product_id": 101}); !res.IsError || calls.Load() != 0 {
		t.Errorf("порожнє оновлення: IsError=%v запитів=%d", res.IsError, calls.Load())
	}
	if res := server.CallTool(t, cs, "update_product", map[string]any{"product_id": 101, "price": 0}); res.IsError {
		t.Error(server.ResultText(res))
	}
}

func TestCategories(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("filter[parent_id]") != "4" {
				t.Errorf("запит %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"total":1,"current_page":1,"data":[{"id":5,"name":"PLA","parent_id":4}]}`)
		case http.MethodPost:
			if body := decodeBody(t, r); body["name"] != "PETG" || body["parent_id"] != float64(4) {
				t.Errorf("тіло: %v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":6,"name":"PETG","parent_id":4}`)
		}
	})
	list := server.Decode[listing.Result[Category]](t, server.CallTool(t, cs, "list_categories", map[string]any{"parent_ids": []int{4}}))
	if len(list.Items) != 1 || list.Items[0].Name != "PLA" || list.Items[0].ParentID == nil || *list.Items[0].ParentID != 4 {
		t.Errorf("категорії: %+v", list)
	}
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_category", map[string]any{"name": "PETG", "parent_id": 4}))
	if out["id"] != float64(6) {
		t.Errorf("категорія: %v", out)
	}
}

func TestCatalogErrorFieldsBecomeIsError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"offers.0.sku":["The selected sku is invalid."]}}`)
	})
	res := server.CallTool(t, cs, "update_offers", map[string]any{"offers": []any{map[string]any{"sku": "NOPE", "price": 1}}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "offers.0.sku: The selected sku is invalid.") {
		t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
	}
}

func TestReadOnlyHidesCatalogWrites(t *testing.T) {
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.NotFoundHandler())}, true)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{"list_products", "get_product", "list_offers", "list_categories"} {
		if !got[name] {
			t.Errorf("%s має бути в read-only", name)
		}
	}
	for _, name := range []string{"create_product", "update_product", "import_products", "create_offers", "update_offers", "create_category"} {
		if got[name] {
			t.Errorf("%s не має бути в read-only", name)
		}
	}
}
