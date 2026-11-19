// SPDX-License-Identifier: AGPL-3.0-only

package stock

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
)

// connect підключає клієнта MCP до групи stock поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

func TestValidateSKURejectsComma(t *testing.T) {
	cases := []struct {
		sku  string
		want string
	}{
		{"PLA-1", ""},
		{"PLA 1 кг", ""},
		{"PLA-1,PLA-2", "SKU must not contain commas"},
		{",", "SKU must not contain commas"},
		{"", "SKU must not be empty"},
		{"  ", "SKU must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.sku, func(t *testing.T) {
			err := ValidateSKU(tc.sku)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("неочікувана помилка: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("помилка %v, очікувалось %q", err, tc.want)
			}
		})
	}
}

func TestFetchStocksPerWarehouse(t *testing.T) {
	c := server.FakeKeyCRM(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/offers/stocks" || q.Get("filter[offers_sku]") != "PLA-1" || q.Get("filter[details]") != "true" || q.Get("limit") != "50" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":1,"current_page":1,"next_page_url":null,"data":[
			{"id":22,"sku":"PLA-1","price":650,"purchased_price":"50.50",
			 "warehouse":[{"id":1,"name":"Київ","quantity":2,"reserve":1},{"id":2,"name":"Львів","quantity":5,"reserve":0}]}]}`)
	}))
	rows, err := FetchStocks(t.Context(), c, []string{"PLA-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := rows["PLA-1"]
	if !ok {
		t.Fatalf("немає PLA-1: %v", rows)
	}
	if !maps.Equal(got.Warehouses, map[int]float64{1: 2, 2: 5}) || got.Quantity != 7 || got.Reserve != 1 ||
		got.OfferID != 22 || got.PurchasedPrice != 50.5 {
		t.Errorf("рядок: %+v", got)
	}
}

func TestFetchStocksPrefersTotalsFromAPI(t *testing.T) {
	c := server.FakeKeyCRM(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":22,"sku":"PLA-1","quantity":0.75,"reserve":0.25,"purchased_price":800,
			"warehouse":[{"id":1,"quantity":0.75,"reserve":0.25}]}]}`)
	}))
	rows, err := FetchStocks(t.Context(), c, []string{"PLA-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows["PLA-1"]; got.Quantity != 0.75 || got.Reserve != 0.25 || got.Warehouses[1] != 0.75 {
		t.Errorf("рядок: %+v", got)
	}
}

func TestFetchStocksBatchesAndPages(t *testing.T) {
	const n = batchSize + 10 // дві пачки SKU
	skus := make([]string, n)
	for i := range skus {
		skus[i] = fmt.Sprintf("SKU-%03d", i)
	}
	var (
		mu       sync.Mutex
		requests []string
	)
	c := server.FakeKeyCRM(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		mu.Lock()
		requests = append(requests, q.Get("filter[offers_sku]")+"|"+q.Get("filter[offers_id]")+"|"+q.Get("page"))
		mu.Unlock()
		if q.Get("filter[details]") != "true" {
			t.Errorf("без details: %s", r.URL.RawQuery)
		}
		var values []string
		if v := q.Get("filter[offers_sku]"); v != "" {
			values = strings.Split(v, ",")
		}
		// Сторінки по 30 рядків: перша пачка (50 SKU) займає дві сторінки.
		const per = 30
		from, to := (page-1)*per, min(page*per, len(values))
		type row struct {
			ID       int     `json:"id"`
			SKU      string  `json:"sku"`
			Quantity float64 `json:"quantity"`
		}
		data := []row{}
		for i := from; i < to; i++ {
			data = append(data, row{ID: 1000 + i, SKU: values[i], Quantity: 1})
		}
		if id := q.Get("filter[offers_id]"); id != "" {
			data = append(data, row{ID: 7, SKU: "BY-ID", Quantity: 3}, row{ID: 8, SKU: "EXTRA"})
		}
		var next *string
		if to < len(values) {
			s := fmt.Sprintf("https://openapi.keycrm.app/v1/offers/stocks?page=%d", page+1)
			next = &s
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "next_page_url": next})
	}))
	rows, err := FetchStocks(t.Context(), c, append(skus, skus[0]), []int{7})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != n+1 {
		t.Errorf("рядків %d, очікувалось %d", len(rows), n+1)
	}
	for _, s := range skus {
		if rows[s].Quantity != 1 {
			t.Errorf("%s: %+v", s, rows[s])
		}
	}
	if rows["#7"].SKU != "BY-ID" || rows["#7"].Quantity != 3 {
		t.Errorf("#7: %+v", rows["#7"])
	}
	if _, ok := rows["#8"]; ok {
		t.Error("незапитаний варіант потрапив у результат")
	}
	// Пачка 1 (50 SKU) — 2 сторінки, пачка 2 (10 SKU) — 1, id — 1.
	if len(requests) != 4 {
		t.Fatalf("запити: %v", requests)
	}
	if got := strings.Count(strings.Split(requests[0], "|")[0], ",") + 1; got != batchSize {
		t.Errorf("у першій пачці %d SKU", got)
	}
}

func TestFetchStocksRejectsCommaBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	c := server.FakeKeyCRM(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	_, err := FetchStocks(t.Context(), c, []string{"PLA-1", "A,B"}, nil)
	if err == nil || !strings.Contains(err.Error(), "SKU must not contain commas") || calls.Load() != 0 {
		t.Errorf("err=%v запитів=%d", err, calls.Load())
	}
}

func TestGetStockReportsMissing(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":22,"sku":"PLA-1","quantity":7,"purchased_price":50,
			"warehouse":[{"id":1,"quantity":2},{"id":2,"quantity":5}]}]}`)
	})
	out := server.Decode[GetStockResult](t, server.CallTool(t, cs, "get_stock", map[string]any{"skus": []string{"PLA-1", "NOPE"}}))
	wantWH := []WarehouseQty{{ID: 1, Quantity: 2}, {ID: 2, Quantity: 5}}
	if len(out.Items) != 1 || !slices.Equal(out.Items[0].Warehouses, wantWH) || out.Items[0].Quantity != 7 || len(out.Missing) != 1 || out.Missing[0] != "NOPE" {
		t.Errorf("результат: %+v", out)
	}
}

func TestGetStockRejectsComma(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "get_stock", map[string]any{"skus": []string{"PLA-1,PLA-2"}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "SKU must not contain commas") || calls.Load() != 0 {
		t.Errorf("IsError=%v текст=%q запитів=%d", res.IsError, server.ResultText(res), calls.Load())
	}
	if res := server.CallTool(t, cs, "get_stock", map[string]any{}); !res.IsError {
		t.Error("без skus і offer_ids має бути IsError")
	}
}

func TestSetStockNeedsConfirm(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/offers/stocks" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body struct {
			WarehouseID int              `json:"warehouse_id"`
			Stocks      []map[string]any `json:"stocks"`
			Confirm     *bool            `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.WarehouseID != 1 || body.Confirm != nil || len(body.Stocks) != 2 {
			t.Errorf("тіло: %+v", body)
		}
		bySKU, byID := body.Stocks[0], body.Stocks[1]
		if bySKU["sku"] != "PLA-1" || bySKU["quantity"] != float64(12) || bySKU["id"] != nil {
			t.Errorf("за sku: %v", bySKU)
		}
		// Нульова кількість — допустима, вона не має губитися.
		if byID["id"] != float64(23) || byID["quantity"] != float64(0) || byID["sku"] != nil {
			t.Errorf("за id: %v", byID)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":true}`)
	})
	stocks := []any{map[string]any{"sku": "PLA-1", "quantity": 12}, map[string]any{"offer_id": 23, "quantity": 0}}
	res := server.CallTool(t, cs, "set_stock", map[string]any{"warehouse_id": 1, "stocks": stocks})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") || calls.Load() != 0 {
		t.Fatalf("без confirm: IsError=%v текст=%q запитів=%d", res.IsError, server.ResultText(res), calls.Load())
	}
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "set_stock", map[string]any{"warehouse_id": 1, "stocks": stocks, "confirm": true}))
	if out["status"] != true || out["submitted"] != float64(2) || calls.Load() != 1 {
		t.Errorf("результат: %v, запитів %d", out, calls.Load())
	}
}

func TestSetStockValidates(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"без складу", map[string]any{"stocks": []any{map[string]any{"sku": "A", "quantity": 1}}}, "warehouse_id"},
		{"і sku, і id", map[string]any{"warehouse_id": 1, "stocks": []any{map[string]any{"sku": "A", "offer_id": 2, "quantity": 1}}}, "exactly one"},
		{"від'ємна кількість", map[string]any{"warehouse_id": 1, "stocks": []any{map[string]any{"sku": "A", "quantity": -1}}}, "negative"},
		{"порожньо", map[string]any{"warehouse_id": 1, "stocks": []any{}}, "stocks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["confirm"] = true
			res := server.CallTool(t, cs, "set_stock", tc.args)
			if !res.IsError || !strings.Contains(server.ResultText(res), tc.want) {
				t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("до валідації зроблено %d запитів", calls.Load())
	}
}

func TestReadOnlyHidesSetStock(t *testing.T) {
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.NotFoundHandler())}, true)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "get_stock" {
		t.Errorf("інструменти read-only: %v", res.Tools)
	}
}
