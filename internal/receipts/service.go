// SPDX-License-Identifier: AGPL-3.0-only

package receipts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/stock"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// Статуси результату.
const (
	// StatusPreview — розрахунок без запису.
	StatusPreview = "preview"
	// StatusDone — залишки й собівартість записано.
	StatusDone = "done"
	// StatusPartial — залишки записано, собівартість — ні.
	StatusPartial = "partial"
)

// MaxItems — найбільше позицій в одній накладній.
const MaxItems = 500

// Item — позиція накладної: рівно одне з SKU чи OfferID.
type Item struct {
	SKU      string  `json:"sku,omitempty" jsonschema:"артикул варіанту (без ком); або offer_id"`
	OfferID  int     `json:"offer_id,omitempty" jsonschema:"ідентифікатор варіанту; або sku"`
	Quantity float64 `json:"quantity" jsonschema:"кількість, > 0, може бути дробовою (0.75 кг)"`
	UnitCost float64 `json:"unit_cost" jsonschema:"закупівельна ціна за одиницю, ≥ 0"`
}

// Receipt — прибуткова накладна. Постачальник, номер, дата й примітка лише
// повертаються в результаті: keyCRM їх не зберігає.
type Receipt struct {
	WarehouseID int    `json:"warehouse_id" jsonschema:"ідентифікатор складу (див. warehouses у get_stock)"`
	Items       []Item `json:"items" jsonschema:"позиції накладної; однакові артикули сумуються"`
	Supplier    string `json:"supplier,omitempty" jsonschema:"постачальник — лише для результату"`
	DocNumber   string `json:"doc_number,omitempty" jsonschema:"номер документа — лише для результату"`
	DocDate     string `json:"doc_date,omitempty" jsonschema:"дата документа, напр. 2026-11-19 — лише для результату"`
	Note        string `json:"note,omitempty" jsonschema:"примітка — лише для результату"`
}

// Line — розрахунок позиції: «було → стане».
type Line struct {
	SKU                string  `json:"sku,omitempty"`
	OfferID            int     `json:"offer_id,omitempty"`
	Quantity           float64 `json:"quantity"`
	UnitCost           float64 `json:"unit_cost"`
	QtyBefore          float64 `json:"qty_before"`
	WarehouseQtyBefore float64 `json:"warehouse_qty_before"`
	CostBefore         float64 `json:"cost_before"`
	CostAfter          float64 `json:"cost_after"`
	WarehouseQtyAfter  float64 `json:"warehouse_qty_after"`
}

// Result — результат накладної чи відкату.
type Result struct {
	Status   string   `json:"status" jsonschema:"preview — нічого не записано; done — записано; partial — залишки записано, собівартість ні (див. warnings)"`
	Receipt  Receipt  `json:"receipt"`
	Lines    []Line   `json:"lines"`
	Warnings []string `json:"warnings,omitempty"`
}

// Service проводить накладні й відкати. Записи виконуються по одному на
// процес: одночасні накладні на той самий варіант рахували б собівартість
// від того самого «було».
type Service struct {
	c   *keycrm.Client
	sem chan struct{}
}

// NewService повертає сервіс поверх клієнта keyCRM.
func NewService(c *keycrm.Client) *Service {
	return &Service{c: c, sem: make(chan struct{}, 1)}
}

// entry — позиція після нормалізації й об'єднання однакових артикулів.
type entry struct {
	key      string // SKU або stock.OfferKey(id)
	sku      string
	offerID  int
	qty      float64
	unitCost float64
}

// plan — розраховані рядки накладної.
type plan struct {
	lines    []Line
	keys     []string
	keepCost []bool // true — собівартість не надсилається
	warnings []string
}

// Preview рахує накладну без запису.
func (s *Service) Preview(ctx context.Context, r Receipt) (Result, error) {
	p, err := s.plan(ctx, r, false)
	if err != nil {
		return Result{}, err
	}
	return Result{Status: StatusPreview, Receipt: r, Lines: p.lines, Warnings: p.warnings}, nil
}

// Apply проводить накладну: перечитує залишки, записує нові кількості на
// склад, потім нову собівартість, і звіряє результат.
func (s *Service) Apply(ctx context.Context, r Receipt) (Result, error) {
	if err := s.lock(ctx); err != nil {
		return Result{}, err
	}
	defer s.unlock()
	p, err := s.plan(ctx, r, false)
	if err != nil {
		return Result{}, err
	}
	return s.write(ctx, r, p, "receive_stock")
}

// Revert відкочує накладну: віднімає кількості зі складу й рахує
// собівартість зворотною формулою. dryRun — лише розрахунок.
func (s *Service) Revert(ctx context.Context, r Receipt, dryRun bool) (Result, error) {
	if dryRun {
		p, err := s.plan(ctx, r, true)
		if err != nil {
			return Result{}, err
		}
		return Result{Status: StatusPreview, Receipt: r, Lines: p.lines, Warnings: p.warnings}, nil
	}
	if err := s.lock(ctx); err != nil {
		return Result{}, err
	}
	defer s.unlock()
	p, err := s.plan(ctx, r, true)
	if err != nil {
		return Result{}, err
	}
	return s.write(ctx, r, p, "revert_receipt")
}

func (s *Service) lock(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) unlock() { <-s.sem }

// normalize перевіряє накладну й об'єднує однакові артикули: кількість
// сумується, ціна зважується за кількістю.
func normalize(r Receipt) ([]entry, error) {
	if r.WarehouseID <= 0 {
		return nil, errors.New("warehouse_id is required")
	}
	if len(r.Items) == 0 || len(r.Items) > MaxItems {
		return nil, fmt.Errorf("items: pass 1 to %d items, got %d", MaxItems, len(r.Items))
	}
	var out []entry
	index := map[string]int{}
	for i, it := range r.Items {
		sku := strings.TrimSpace(it.SKU)
		if (it.OfferID != 0) == (sku != "") || it.OfferID < 0 {
			return nil, fmt.Errorf("items[%d]: set exactly one of sku or offer_id", i)
		}
		if sku != "" {
			if err := stock.ValidateSKU(sku); err != nil {
				return nil, fmt.Errorf("items[%d]: %w", i, err)
			}
		}
		if !(it.Quantity > 0) || math.IsInf(it.Quantity, 0) {
			return nil, fmt.Errorf("items[%d]: quantity must be greater than 0", i)
		}
		if !(it.UnitCost >= 0) || math.IsInf(it.UnitCost, 0) {
			return nil, fmt.Errorf("items[%d]: unit_cost must not be negative", i)
		}
		key := sku
		if sku == "" {
			key = stock.OfferKey(it.OfferID)
		}
		if j, ok := index[key]; ok {
			e := &out[j]
			e.unitCost = (e.qty*e.unitCost + it.Quantity*it.UnitCost) / (e.qty + it.Quantity)
			e.qty = cleanQty(e.qty + it.Quantity)
			continue
		}
		index[key] = len(out)
		out = append(out, entry{key: key, sku: sku, offerID: it.OfferID, qty: it.Quantity, unitCost: it.UnitCost})
	}
	return out, nil
}

// fetch читає поточні залишки всіх позицій.
func (s *Service) fetch(ctx context.Context, entries []entry) (map[string]stock.StockRow, error) {
	var skus []string
	var ids []int
	for _, e := range entries {
		if e.sku != "" {
			skus = append(skus, e.sku)
		} else {
			ids = append(ids, e.offerID)
		}
	}
	return stock.FetchStocks(ctx, s.c, skus, ids)
}

// plan перевіряє накладну, читає залишки й рахує «було → стане». Помилка
// означає, що нічого не записано.
func (s *Service) plan(ctx context.Context, r Receipt, revert bool) (plan, error) {
	entries, err := normalize(r)
	if err != nil {
		return plan{}, err
	}
	rows, err := s.fetch(ctx, entries)
	if err != nil {
		return plan{}, err
	}
	var missing, negative []string
	byOffer := map[int]string{}
	whSeen := false
	p := plan{}
	for _, e := range entries {
		row, ok := rows[e.key]
		if !ok {
			missing = append(missing, e.key)
			continue
		}
		if prev, dup := byOffer[row.OfferID]; dup {
			return plan{}, fmt.Errorf("offer %d is listed twice (%s and %s): put it once", row.OfferID, prev, e.key)
		}
		byOffer[row.OfferID] = e.key
		qw, onWarehouse := row.Warehouses[r.WarehouseID]
		whSeen = whSeen || onWarehouse
		l := Line{SKU: e.sku, OfferID: e.offerID, Quantity: e.qty, UnitCost: e.unitCost,
			QtyBefore: row.Quantity, WarehouseQtyBefore: qw, CostBefore: row.PurchasedPrice}
		keep := false
		if !revert {
			l.CostAfter = WeightedCost(row.Quantity, row.PurchasedPrice, e.qty, e.unitCost)
			l.WarehouseQtyAfter = cleanQty(qw + e.qty)
		} else {
			l.WarehouseQtyAfter = cleanQty(qw - e.qty)
			if l.WarehouseQtyAfter < 0 {
				negative = append(negative, fmt.Sprintf("%s (on warehouse %s, revert %s)", e.key, num(qw), num(e.qty)))
			}
			c, err := ReverseCost(row.Quantity, row.PurchasedPrice, e.qty, e.unitCost)
			switch {
			case errors.Is(err, ErrStockToZero):
				keep = true
				p.warnings = append(p.warnings, e.key+": stock goes to zero, purchased_price left unchanged")
			case errors.Is(err, ErrNegativeCost):
				keep = true
				p.warnings = append(p.warnings, e.key+": reverse cost would be negative (other movements since the receipt?), purchased_price left unchanged")
			case err != nil:
				return plan{}, err
			}
			l.CostAfter = c
			if keep {
				l.CostAfter = row.PurchasedPrice
			}
		}
		p.lines = append(p.lines, l)
		p.keys = append(p.keys, e.key)
		p.keepCost = append(p.keepCost, keep)
	}
	if len(missing) > 0 {
		return plan{}, fmt.Errorf("not found in keyCRM: %s; nothing was written", strings.Join(missing, ", "))
	}
	if len(negative) > 0 {
		return plan{}, fmt.Errorf("warehouse stock would go below zero: %s; nothing was written", strings.Join(negative, ", "))
	}
	if !whSeen {
		p.warnings = append(p.warnings, fmt.Sprintf("warehouse %d has none of these offers yet: check warehouse_id with get_stock", r.WarehouseID))
	}
	return p, nil
}

// write записує залишки, потім собівартість, і звіряє результат.
func (s *Service) write(ctx context.Context, r Receipt, p plan, tool string) (Result, error) {
	res := Result{Status: StatusDone, Receipt: r, Lines: p.lines, Warnings: p.warnings}

	stocks := make([]map[string]any, len(p.lines))
	var offers []map[string]any
	var costs []string
	for i, l := range p.lines {
		stocks[i] = ref(l)
		stocks[i]["quantity"] = l.WarehouseQtyAfter
		if !p.keepCost[i] {
			o := ref(l)
			o["purchased_price"] = l.CostAfter
			offers = append(offers, o)
			costs = append(costs, p.keys[i]+"="+num(l.CostAfter))
		}
	}
	if err := s.put(ctx, map[string]any{"warehouse_id": r.WarehouseID, "stocks": stocks}, true); err != nil {
		return Result{}, fmt.Errorf("update stock (nothing was changed): %w", err)
	}
	if len(offers) > 0 {
		if err := s.put(ctx, map[string]any{"offers": offers}, false); err != nil {
			res.Status = StatusPartial
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"stock was updated but purchased_price was not (%v): do not re-run %s; set these costs with update_offers: %s",
				err, tool, strings.Join(costs, ", ")))
			return res, nil
		}
	}
	res.Warnings = append(res.Warnings, s.verify(ctx, r, p)...)
	return res, nil
}

// verify перечитує залишки й повертає розбіжності з очікуваним.
func (s *Service) verify(ctx context.Context, r Receipt, p plan) []string {
	entries := make([]entry, len(p.lines))
	for i, l := range p.lines {
		entries[i] = entry{key: p.keys[i], sku: l.SKU, offerID: l.OfferID}
	}
	rows, err := s.fetch(ctx, entries)
	if err != nil {
		return []string{fmt.Sprintf("written, but could not re-read stock to verify: %v", err)}
	}
	var out []string
	for i, l := range p.lines {
		row, ok := rows[p.keys[i]]
		if !ok {
			out = append(out, p.keys[i]+": not found when re-reading stock")
			continue
		}
		if now := row.Warehouses[r.WarehouseID]; math.Abs(now-l.WarehouseQtyAfter) > qtyEpsilon {
			out = append(out, fmt.Sprintf("%s: expected %s on warehouse, now %s (concurrent movement)", p.keys[i], num(l.WarehouseQtyAfter), num(now)))
		}
		if !p.keepCost[i] && math.Abs(row.PurchasedPrice-l.CostAfter) >= 0.005 {
			out = append(out, fmt.Sprintf("%s: expected purchased_price %s, now %s", p.keys[i], num(l.CostAfter), num(row.PurchasedPrice)))
		}
	}
	return out
}

// put виконує offersStocksUpdate (stocks) або offersUpdate.
func (s *Service) put(ctx context.Context, body any, stocks bool) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	if stocks {
		_, err = listing.Object(s.c.API.OffersStocksUpdateWithBody(ctx, "application/json", bytes.NewReader(b)))
	} else {
		_, err = listing.Object(s.c.API.OffersUpdateWithBody(ctx, "application/json", bytes.NewReader(b)))
	}
	return err
}

// ref — ідентифікатор варіанту для тіла запису: sku або id, як у накладній.
func ref(l Line) map[string]any {
	if l.SKU != "" {
		return map[string]any{"sku": l.SKU}
	}
	return map[string]any{"id": l.OfferID}
}

// qtyEpsilon — допуск порівняння кількостей.
const qtyEpsilon = 1e-6

// cleanQty прибирає шум двійкової арифметики (0.1+0.2) без округлення
// значущих знаків: дробові кількості (0.75 кг) лишаються як є.
func cleanQty(v float64) float64 {
	r := math.Round(v/qtyEpsilon) / (1 / qtyEpsilon)
	if r == 0 {
		return 0
	}
	return r
}

// num форматує число без зайвих нулів.
func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
