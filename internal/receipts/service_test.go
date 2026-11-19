// SPDX-License-Identifier: AGPL-3.0-only

package receipts

import (
	"strings"
	"testing"
)

func pla(qty, cost float64) *fakeOffer {
	return &fakeOffer{ID: 22, SKU: "PLA-1", Cost: cost, Warehouses: map[int]float64{1: qty}}
}

func receipt(items ...Item) Receipt {
	return Receipt{WarehouseID: 1, Items: items}
}

func TestPreviewDoesNotWrite(t *testing.T) {
	f := newFake(t, pla(2, 50))
	res, err := NewService(f.client()).Preview(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}))
	if err != nil {
		t.Fatal(err)
	}
	if f.writes() != 0 {
		t.Errorf("preview записав %d разів", f.writes())
	}
	want := Line{SKU: "PLA-1", Quantity: 1, UnitCost: 59, QtyBefore: 2, WarehouseQtyBefore: 2, CostBefore: 50, CostAfter: 53, WarehouseQtyAfter: 3}
	if res.Status != StatusPreview || len(res.Lines) != 1 || res.Lines[0] != want {
		t.Errorf("результат: %+v", res)
	}
}

func TestApplyUpdatesStockAndCost(t *testing.T) {
	f := newFake(t, pla(2, 50))
	res, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone || len(res.Warnings) != 0 {
		t.Errorf("результат: %+v", res)
	}
	if len(f.stockPuts) != 1 || f.stockPuts[0].WarehouseID != 1 || len(f.stockPuts[0].Stocks) != 1 ||
		f.stockPuts[0].Stocks[0].SKU != "PLA-1" || f.stockPuts[0].Stocks[0].Quantity != 3 {
		t.Errorf("PUT /offers/stocks: %+v", f.stockPuts)
	}
	if len(f.offerPuts) != 1 || len(f.offerPuts[0].Offers) != 1 ||
		f.offerPuts[0].Offers[0]["sku"] != "PLA-1" || f.offerPuts[0].Offers[0]["purchased_price"] != float64(53) {
		t.Errorf("PUT /offers: %+v", f.offerPuts)
	}
	// Бюджет запитів: читання до запису і звірка після.
	if f.gets != 2 {
		t.Errorf("читань залишків %d, очікувалось 2", f.gets)
	}
}

func TestApplyUsesTotalQtyForCostAndWarehouseQtyForStock(t *testing.T) {
	f := newFake(t, &fakeOffer{ID: 22, SKU: "PLA-1", Cost: 50, Warehouses: map[int]float64{1: 2, 2: 4}})
	res, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 2, UnitCost: 80}))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.stockPuts[0].Stocks[0].Quantity; got != 4 {
		t.Errorf("склад 1: %v, очікувалось 4", got)
	}
	if got := f.offerPuts[0].Offers[0]["purchased_price"]; got != 57.5 {
		t.Errorf("собівартість %v, очікувалось 57.5", got)
	}
	if l := res.Lines[0]; l.QtyBefore != 6 || l.WarehouseQtyBefore != 2 || l.WarehouseQtyAfter != 4 || f.offers[0].Warehouses[2] != 4 {
		t.Errorf("рядок %+v, склади %v", l, f.offers[0].Warehouses)
	}
}

func TestApplyMergesDuplicateSKUs(t *testing.T) {
	f := newFake(t, pla(2, 50))
	res, err := NewService(f.client()).Apply(t.Context(), receipt(
		Item{SKU: "PLA-1", Quantity: 1, UnitCost: 50},
		Item{SKU: " PLA-1 ", Quantity: 1, UnitCost: 70},
	))
	if err != nil {
		t.Fatal(err)
	}
	// 2 по 50 + (1 по 50 + 1 по 70 = 2 по 60) → 4 по 55.
	if len(res.Lines) != 1 || res.Lines[0].Quantity != 2 || res.Lines[0].UnitCost != 60 || res.Lines[0].CostAfter != 55 {
		t.Errorf("рядки: %+v", res.Lines)
	}
	if len(f.stockPuts[0].Stocks) != 1 || f.stockPuts[0].Stocks[0].Quantity != 4 {
		t.Errorf("PUT /offers/stocks: %+v", f.stockPuts)
	}
}

func TestApplyFractionalQuantities(t *testing.T) {
	f := newFake(t, pla(0.75, 800))
	res, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1.25, UnitCost: 760}))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.stockPuts[0].Stocks[0].Quantity; got != 2 {
		t.Errorf("склад: %v, очікувалось 2", got)
	}
	if res.Lines[0].CostAfter != 775 {
		t.Errorf("собівартість %v, очікувалось 775", res.Lines[0].CostAfter)
	}
}

func TestApplyByOfferID(t *testing.T) {
	f := newFake(t, pla(2, 50))
	if _, err := NewService(f.client()).Apply(t.Context(), receipt(Item{OfferID: 22, Quantity: 1, UnitCost: 59})); err != nil {
		t.Fatal(err)
	}
	if s := f.stockPuts[0].Stocks[0]; s.ID != 22 || s.SKU != "" || s.Quantity != 3 {
		t.Errorf("PUT /offers/stocks: %+v", s)
	}
	if o := f.offerPuts[0].Offers[0]; o["id"] != float64(22) || o["sku"] != nil || o["purchased_price"] != float64(53) {
		t.Errorf("PUT /offers: %v", o)
	}
}

func TestApplyRejectsUnknownSKU(t *testing.T) {
	f := newFake(t, pla(2, 50))
	svc := NewService(f.client())
	r := receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}, Item{SKU: "NOPE", Quantity: 1, UnitCost: 10}, Item{OfferID: 99, Quantity: 1, UnitCost: 10})
	for name, call := range map[string]func() error{
		"preview": func() error { _, err := svc.Preview(t.Context(), r); return err },
		"apply":   func() error { _, err := svc.Apply(t.Context(), r); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "NOPE") || !strings.Contains(err.Error(), "#99") || strings.Contains(err.Error(), "PLA-1") {
			t.Errorf("%s: помилка %v", name, err)
		}
	}
	if f.writes() != 0 {
		t.Errorf("записів %d", f.writes())
	}
}

func TestApplyRejectsSKUWithComma(t *testing.T) {
	f := newFake(t, pla(2, 50))
	_, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1,PLA-2", Quantity: 1, UnitCost: 59}))
	if err == nil || !strings.Contains(err.Error(), "SKU must not contain commas") {
		t.Errorf("помилка %v", err)
	}
	if f.gets != 0 || f.writes() != 0 {
		t.Errorf("запитів: читань %d, записів %d", f.gets, f.writes())
	}
}

func TestValidateReceipt(t *testing.T) {
	cases := []struct {
		name string
		r    Receipt
		want string
	}{
		{"без складу", Receipt{Items: []Item{{SKU: "A", Quantity: 1}}}, "warehouse_id"},
		{"без позицій", Receipt{WarehouseID: 1}, "items"},
		{"і sku, і offer_id", receipt(Item{SKU: "A", OfferID: 2, Quantity: 1}), "exactly one"},
		{"ні sku, ні offer_id", receipt(Item{Quantity: 1}), "exactly one"},
		{"нульова кількість", receipt(Item{SKU: "A"}), "quantity"},
		{"від'ємна ціна", receipt(Item{SKU: "A", Quantity: 1, UnitCost: -1}), "unit_cost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			_, err := NewService(f.client()).Preview(t.Context(), tc.r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("помилка %v, очікувалось %q", err, tc.want)
			}
			if f.gets != 0 {
				t.Errorf("до валідації %d запитів", f.gets)
			}
		})
	}
}

func TestApplyRejectsSameOfferBySKUAndID(t *testing.T) {
	f := newFake(t, pla(2, 50))
	_, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}, Item{OfferID: 22, Quantity: 1, UnitCost: 59}))
	if err == nil || !strings.Contains(err.Error(), "offer 22") || f.writes() != 0 {
		t.Errorf("помилка %v, записів %d", err, f.writes())
	}
}

func TestApplyPartialWhenOffersUpdateFails(t *testing.T) {
	f := newFake(t, pla(2, 50))
	f.failOffers = true
	res, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}))
	if err != nil {
		t.Fatalf("partial не має бути Go-помилкою: %v", err)
	}
	if res.Status != StatusPartial || len(res.Warnings) == 0 {
		t.Fatalf("результат: %+v", res)
	}
	w := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"purchased_price was not", "do not re-run receive_stock", "update_offers", "PLA-1=53"} {
		if !strings.Contains(w, want) {
			t.Errorf("у попередженнях немає %q: %s", want, w)
		}
	}
	if f.offers[0].Warehouses[1] != 3 {
		t.Errorf("залишок %v", f.offers[0].Warehouses)
	}
}

func TestApplyFailsCleanlyWhenStockUpdateFails(t *testing.T) {
	f := newFake(t, pla(2, 50))
	f.failStocks = true
	_, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}))
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("помилка %v", err)
	}
	if len(f.offerPuts) != 0 {
		t.Errorf("після збою залишків оновлено собівартість: %+v", f.offerPuts)
	}
}

func TestApplyWarnsOnConcurrentMovement(t *testing.T) {
	f := newFake(t, pla(2, 50))
	f.afterStockPut = func(f *fakeKeyCRM) { f.offers[0].Warehouses[1]-- } // відвантажили 1 шт
	res, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}))
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(res.Warnings, "\n")
	if res.Status != StatusDone || !strings.Contains(w, "PLA-1: expected 3 on warehouse, now 2 (concurrent movement)") {
		t.Errorf("результат: %+v", res)
	}
}

func TestApplyWarnsWhenCostDiffersAfterWrite(t *testing.T) {
	f := newFake(t, pla(2, 50))
	f.afterOfferPut = func(f *fakeKeyCRM) { f.offers[0].Cost = 50 } // keyCRM не застосував нову ціну
	res, err := NewService(f.client()).Apply(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "PLA-1: expected purchased_price 53, now 50") {
		t.Errorf("попередження: %v", res.Warnings)
	}
}

func TestRevertComputesReverseCost(t *testing.T) {
	f := newFake(t, pla(3, 53))
	res, err := NewService(f.client()).Revert(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone || f.stockPuts[0].Stocks[0].Quantity != 2 || f.offerPuts[0].Offers[0]["purchased_price"] != float64(50) {
		t.Errorf("результат %+v; stocks %+v; offers %+v", res, f.stockPuts, f.offerPuts)
	}
}

func TestRevertDryRunDoesNotWrite(t *testing.T) {
	f := newFake(t, pla(3, 53))
	res, err := NewService(f.client()).Revert(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPreview || res.Lines[0].CostAfter != 50 || res.Lines[0].WarehouseQtyAfter != 2 || f.writes() != 0 {
		t.Errorf("результат %+v, записів %d", res, f.writes())
	}
}

func TestRevertToZeroKeepsCost(t *testing.T) {
	f := newFake(t, pla(1, 59))
	res, err := NewService(f.client()).Revert(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59}), false)
	if err != nil {
		t.Fatal(err)
	}
	if f.stockPuts[0].Stocks[0].Quantity != 0 {
		t.Errorf("PUT /offers/stocks: %+v", f.stockPuts)
	}
	if len(f.offerPuts) != 0 {
		t.Errorf("purchased_price не мав надсилатися: %+v", f.offerPuts)
	}
	if res.Lines[0].CostAfter != 59 || !strings.Contains(strings.Join(res.Warnings, "\n"), "PLA-1: stock goes to zero, purchased_price left unchanged") {
		t.Errorf("результат: %+v", res)
	}
}

func TestRevertNegativeCostKeepsCost(t *testing.T) {
	f := newFake(t, pla(2, 50))
	res, err := NewService(f.client()).Revert(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 200}), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.offerPuts) != 0 || res.Lines[0].CostAfter != 50 || !strings.Contains(strings.Join(res.Warnings, "\n"), "reverse cost would be negative") {
		t.Errorf("результат %+v, offers %+v", res, f.offerPuts)
	}
}

func TestRevertRejectsNegativeWarehouseStock(t *testing.T) {
	f := newFake(t, &fakeOffer{ID: 22, SKU: "PLA-1", Cost: 50, Warehouses: map[int]float64{1: 1, 2: 5}})
	_, err := NewService(f.client()).Revert(t.Context(), receipt(Item{SKU: "PLA-1", Quantity: 2, UnitCost: 50}), false)
	if err == nil || !strings.Contains(err.Error(), "PLA-1") || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("помилка %v", err)
	}
	if f.writes() != 0 {
		t.Errorf("записів %d", f.writes())
	}
}

func TestResultEchoesSupplierAndDocNumber(t *testing.T) {
	f := newFake(t, pla(2, 50))
	r := receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59})
	r.Supplier, r.DocNumber, r.DocDate, r.Note = "ТОВ Пластик", "ПН-17", "2026-11-19", "партія з Ali"
	for name, call := range map[string]func() (Result, error){
		"preview": func() (Result, error) { return NewService(f.client()).Preview(t.Context(), r) },
		"apply":   func() (Result, error) { return NewService(f.client()).Apply(t.Context(), r) },
	} {
		res, err := call()
		if err != nil {
			t.Fatal(err)
		}
		if res.Receipt.Supplier != r.Supplier || res.Receipt.DocNumber != r.DocNumber || res.Receipt.DocDate != r.DocDate || res.Receipt.Note != r.Note {
			t.Errorf("%s: %+v", name, res.Receipt)
		}
	}
}

func TestPreviewWarnsAboutUnknownWarehouse(t *testing.T) {
	f := newFake(t, pla(2, 50))
	r := receipt(Item{SKU: "PLA-1", Quantity: 1, UnitCost: 59})
	r.WarehouseID = 7
	res, err := NewService(f.client()).Preview(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "warehouse 7") {
		t.Errorf("попередження: %v", res.Warnings)
	}
}
