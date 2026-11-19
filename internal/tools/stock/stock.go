// SPDX-License-Identifier: AGPL-3.0-only

// Package stock — залишки keyCRM: перегляд по складах і встановлення
// кількості на складі. FetchStocks і ValidateSKU використовують прибуткові
// накладні (receive_stock).
package stock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група stock.
var Toolset = server.Toolset{Name: "stock", Register: register}

const (
	// batchSize — скільки SKU чи id іде в один фільтр: рядок запиту лишається
	// коротким, а пачка вміщається в одну сторінку.
	batchSize = keycrm.MaxPageSize
	// maxRowsPerBatch — запобіжник від нескінченного гортання сторінок однієї пачки.
	maxRowsPerBatch = 1000
	// maxLookup — найбільше SKU та id разом в одному get_stock.
	maxLookup = 200
	// maxSetStock — найбільше позицій в одному set_stock (ліміт keyCRM).
	maxSetStock = 10000
)

// StockRow — залишок варіанту товару: загальний, резерв, собівартість і
// кількість на кожному складі (ключ — id складу).
type StockRow struct { //nolint:revive // назву зафіксовано планом: її споживає internal/receipts
	OfferID        int
	SKU            string
	Quantity       float64
	Reserve        float64
	PurchasedPrice float64
	Warehouses     map[int]float64
}

// ValidateSKU перевіряє артикул перед запитом до keyCRM: порожній артикул і
// артикул з комою відхиляються — кома розділяє значення фільтра, тож такий
// артикул тихо перетворився б на запит двох інших.
func ValidateSKU(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("SKU must not be empty")
	}
	if strings.Contains(s, ",") {
		return fmt.Errorf("SKU %q: SKU must not contain commas", s)
	}
	return nil
}

// rawStock — рядок getPaginatedListOfStocks з filter[details]=true.
type rawStock struct {
	ID             int        `json:"id"`
	SKU            *string    `json:"sku"`
	PurchasedPrice flexFloat  `json:"purchased_price"`
	Quantity       *flexFloat `json:"quantity"`
	Reserve        *flexFloat `json:"reserve"`
	Warehouse      []struct {
		ID       int       `json:"id"`
		Quantity flexFloat `json:"quantity"`
		Reserve  flexFloat `json:"reserve"`
	} `json:"warehouse"`
}

// row перетворює відповідь keyCRM на StockRow. Якщо загальної кількості чи
// резерву у відповіді немає, вони рахуються як сума по складах.
func (r rawStock) row() StockRow {
	out := StockRow{OfferID: r.ID, PurchasedPrice: float64(r.PurchasedPrice), Warehouses: make(map[int]float64, len(r.Warehouse))}
	if r.SKU != nil {
		out.SKU = *r.SKU
	}
	var qty, reserve float64
	for _, w := range r.Warehouse {
		out.Warehouses[w.ID] += float64(w.Quantity)
		qty += float64(w.Quantity)
		reserve += float64(w.Reserve)
	}
	out.Quantity, out.Reserve = qty, reserve
	if r.Quantity != nil {
		out.Quantity = float64(*r.Quantity)
	}
	if r.Reserve != nil {
		out.Reserve = float64(*r.Reserve)
	}
	return out
}

// FetchStocks читає залишки за артикулами skus і id варіантів offerIDs з
// розкладом по складах (filter[details]=true): пачками по 50 значень у фільтрі,
// сторінками по 50 рядків. Ключ мапи — SKU, а для offerIDs — "#<id>".
// Варіантів, яких немає в keyCRM, у мапі немає; артикули порівнюються точно.
func FetchStocks(ctx context.Context, c *keycrm.Client, skus []string, offerIDs []int) (map[string]StockRow, error) {
	for _, s := range skus {
		if err := ValidateSKU(s); err != nil {
			return nil, err
		}
	}
	out := make(map[string]StockRow, len(skus)+len(offerIDs))
	wantSKU := map[string]bool{}
	for _, s := range skus {
		wantSKU[s] = true
	}
	for batch := range slices.Chunk(slices.Sorted(maps.Keys(wantSKU)), batchSize) {
		rows, err := fetchBatch(ctx, c, "offers_sku", strings.Join(batch, ","))
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if wantSKU[r.SKU] {
				out[r.SKU] = r
			}
		}
	}
	wantID := map[int]bool{}
	for _, id := range offerIDs {
		if id <= 0 {
			return nil, fmt.Errorf("offer id must be positive, got %d", id)
		}
		wantID[id] = true
	}
	for batch := range slices.Chunk(slices.Sorted(maps.Keys(wantID)), batchSize) {
		parts := make([]string, len(batch))
		for i, id := range batch {
			parts[i] = strconv.Itoa(id)
		}
		rows, err := fetchBatch(ctx, c, "offers_id", strings.Join(parts, ","))
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if wantID[r.OfferID] {
				out[OfferKey(r.OfferID)] = r
			}
		}
	}
	return out, nil
}

// OfferKey — ключ мапи FetchStocks для варіанту, запитаного за id.
func OfferKey(id int) string { return "#" + strconv.Itoa(id) }

// fetchBatch читає всі сторінки залишків за одним фільтром.
func fetchBatch(ctx context.Context, c *keycrm.Client, field, value string) ([]StockRow, error) {
	raw, err := keycrm.ListAll(ctx, maxRowsPerBatch, func(ctx context.Context, page int) ([]rawStock, bool, error) {
		limit := keycrm.MaxPageSize
		filter := map[string]any{field: value, "details": "true"}
		params := &api.GetPaginatedListOfStocksParams{Page: &page, Limit: &limit, Filter: &filter}
		status, body, err := listing.Raw(c.API.GetPaginatedListOfStocks(ctx, params))
		if err != nil {
			return nil, false, err
		}
		res, err := listing.Decode[rawStock](status, body, page, "")
		if err != nil {
			return nil, false, err
		}
		return res.Items, res.HasNext, nil
	})
	if err != nil {
		return nil, fmt.Errorf("read stocks: %w", err)
	}
	rows := make([]StockRow, len(raw))
	for i, r := range raw {
		rows[i] = r.row()
	}
	return rows, nil
}

// GetStockInput — вхід get_stock.
type GetStockInput struct {
	SKUs     []string `json:"skus,omitempty" jsonschema:"артикули варіантів (без ком)"`
	OfferIDs []int    `json:"offer_ids,omitempty" jsonschema:"ідентифікатори варіантів (list_offers)"`
}

// GetStockResult — вихід get_stock.
type GetStockResult struct {
	Items   []Item   `json:"items"`
	Missing []string `json:"missing,omitempty"`
}

// Item — StockRow для моделі: склади списком, бо JSON Schema виходу
// не описує мапу з числовими ключами.
type Item struct {
	OfferID        int            `json:"offer_id"`
	SKU            string         `json:"sku"`
	Quantity       float64        `json:"quantity"`
	Reserve        float64        `json:"reserve"`
	PurchasedPrice float64        `json:"purchased_price"`
	Warehouses     []WarehouseQty `json:"warehouses"`
}

// WarehouseQty — кількість варіанту на складі.
type WarehouseQty struct {
	ID       int     `json:"id"`
	Quantity float64 `json:"quantity"`
}

func (r StockRow) item() Item {
	it := Item{OfferID: r.OfferID, SKU: r.SKU, Quantity: r.Quantity, Reserve: r.Reserve, PurchasedPrice: r.PurchasedPrice,
		Warehouses: make([]WarehouseQty, 0, len(r.Warehouses))}
	for _, id := range slices.Sorted(maps.Keys(r.Warehouses)) {
		it.Warehouses = append(it.Warehouses, WarehouseQty{ID: id, Quantity: r.Warehouses[id]})
	}
	return it
}

// SetLine — позиція set_stock: рівно одне з sku чи offer_id і нова кількість.
type SetLine struct {
	SKU      string  `json:"sku,omitempty" jsonschema:"артикул варіанту; або offer_id"`
	OfferID  int     `json:"offer_id,omitempty" jsonschema:"ідентифікатор варіанту; або sku"`
	Quantity float64 `json:"quantity" jsonschema:"нова кількість на складі (не приріст), ≥ 0, може бути дробовою"`
}

// SetStockInput — вхід set_stock.
type SetStockInput struct {
	WarehouseID int       `json:"warehouse_id" jsonschema:"ідентифікатор складу (див. warehouses у get_stock)"`
	Stocks      []SetLine `json:"stocks" jsonschema:"позиції з новою кількістю"`
	Confirm     bool      `json:"confirm,omitempty" jsonschema:"true — лише після того, як користувач побачив, що зміниться, і погодився"`
}

// stockBody — тіло offersStocksUpdate.
type stockBody struct {
	WarehouseID int             `json:"warehouse_id"`
	Stocks      []stockBodyLine `json:"stocks"`
}

type stockBodyLine struct {
	ID       int     `json:"id,omitempty"`
	SKU      string  `json:"sku,omitempty"`
	Quantity float64 `json:"quantity"`
}

func validateSetStock(in SetStockInput) error {
	if in.WarehouseID <= 0 {
		return errors.New("warehouse_id is required")
	}
	if len(in.Stocks) == 0 || len(in.Stocks) > maxSetStock {
		return fmt.Errorf("stocks: pass 1 to %d items, got %d", maxSetStock, len(in.Stocks))
	}
	for i, s := range in.Stocks {
		if (s.OfferID > 0) == (strings.TrimSpace(s.SKU) != "") {
			return fmt.Errorf("stocks[%d]: set exactly one of sku or offer_id", i)
		}
		if s.Quantity < 0 {
			return fmt.Errorf("stocks[%d]: quantity must not be negative", i)
		}
	}
	return nil
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "get_stock",
		Description: "Залишки варіантів за артикулами чи id: загальна кількість, резерв, закупівельна ціна (собівартість) і кількість на кожному складі " +
			"(warehouses: id складу → кількість). Артикули, яких немає в keyCRM, — у missing. До 200 значень за раз. Приклади: " +
			`{"skus": ["PLA-BLK-1KG", "PETG-WHT-1KG"]}; {"offer_ids": [22, 23]}.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfStocks"},
	}, func(ctx context.Context, in GetStockInput) (GetStockResult, error) {
		n := len(in.SKUs) + len(in.OfferIDs)
		if n == 0 || n > maxLookup {
			return GetStockResult{}, fmt.Errorf("pass 1 to %d values in skus and offer_ids together, got %d", maxLookup, n)
		}
		rows, err := FetchStocks(ctx, d.KeyCRM, in.SKUs, in.OfferIDs)
		if err != nil {
			return GetStockResult{}, err
		}
		res := GetStockResult{Items: []Item{}}
		seen := map[string]bool{}
		add := func(key string) {
			if seen[key] {
				return
			}
			seen[key] = true
			if row, ok := rows[key]; ok {
				res.Items = append(res.Items, row.item())
			} else {
				res.Missing = append(res.Missing, key)
			}
		}
		for _, s := range in.SKUs {
			add(s)
		}
		for _, id := range in.OfferIDs {
			add(OfferKey(id))
		}
		return res, nil
	})

	server.Add(r, server.ToolSpec{
		Name: "set_stock",
		Description: "Встановлює кількість варіантів на складі (не додає, а замінює): keyCRM створює рух для приведення залишку до вказаної кількості. " +
			"Для надходження товару із закупівельною ціною використовуйте receive_stock. Спершу покажіть користувачу поточні залишки (get_stock) і нові; " +
			"викликайте з confirm: true лише після його згоди. Приклад: " +
			`{"warehouse_id": 1, "stocks": [{"sku": "PLA-BLK-1KG", "quantity": 12}, {"offer_id": 23, "quantity": 0.75}], "confirm": true}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"offersStocksUpdate"},
	}, func(ctx context.Context, in SetStockInput) (map[string]any, error) {
		if err := server.RequireConfirm(in.Confirm, "set_stock"); err != nil {
			return nil, err
		}
		if err := validateSetStock(in); err != nil {
			return nil, err
		}
		body := stockBody{WarehouseID: in.WarehouseID, Stocks: make([]stockBodyLine, len(in.Stocks))}
		for i, s := range in.Stocks {
			body.Stocks[i] = stockBodyLine{ID: s.OfferID, SKU: strings.TrimSpace(s.SKU), Quantity: s.Quantity}
		}
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		obj, err := listing.Object(d.KeyCRM.API.OffersStocksUpdateWithBody(ctx, "application/json", bytes.NewReader(b)))
		if err != nil {
			return nil, err
		}
		obj["submitted"] = len(in.Stocks)
		return obj, nil
	})
}

// flexFloat приймає число як JSON-число або рядок ("123.50"): числа в keyCRM
// трапляються в обох видах.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("number %s: %w", b, err)
	}
	*f = flexFloat(v)
	return nil
}
