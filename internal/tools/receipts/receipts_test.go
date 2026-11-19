// SPDX-License-Identifier: AGPL-3.0-only

package receipts

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/receipts"
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
)

// fakeStock — keyCRM з одним варіантом: 2 шт по 50 на складі 1. Рахує записи.
func fakeStock(t *testing.T, puts *atomic.Int32) *mcp.ClientSession {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			_, _ = io.WriteString(w, `{"status":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":22,"sku":"PLA-1","purchased_price":50,"warehouse":[{"id":1,"quantity":2}]}]}`)
	})
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

var item = []any{map[string]any{"sku": "PLA-1", "quantity": 1, "unit_cost": 59}}

func TestReceiveStockDefaultsToDryRun(t *testing.T) {
	var puts atomic.Int32
	cs := fakeStock(t, &puts)
	out := server.Decode[receipts.Result](t, server.CallTool(t, cs, "receive_stock", map[string]any{"warehouse_id": 1, "items": item, "supplier": "ТОВ Пластик"}))
	if out.Status != receipts.StatusPreview || out.Lines[0].CostAfter != 53 || out.Receipt.Supplier != "ТОВ Пластик" || puts.Load() != 0 {
		t.Errorf("результат %+v, записів %d", out, puts.Load())
	}
}

func TestReceiveStockApplyNeedsConfirm(t *testing.T) {
	var puts atomic.Int32
	cs := fakeStock(t, &puts)
	res := server.CallTool(t, cs, "receive_stock", map[string]any{"warehouse_id": 1, "items": item, "dry_run": false})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") || puts.Load() != 0 {
		t.Fatalf("без confirm: IsError=%v текст=%q записів=%d", res.IsError, server.ResultText(res), puts.Load())
	}
	out := server.Decode[receipts.Result](t, server.CallTool(t, cs, "receive_stock", map[string]any{"warehouse_id": 1, "items": item, "dry_run": false, "confirm": true}))
	// Фейк не змінює стан, тож звірка покаже розбіжність — тут перевіряємо лише запис.
	if out.Status != receipts.StatusDone || puts.Load() != 2 {
		t.Errorf("результат %+v, записів %d", out, puts.Load())
	}
}

func TestRevertReceiptDefaultsToDryRunAndNeedsConfirm(t *testing.T) {
	var puts atomic.Int32
	cs := fakeStock(t, &puts)
	out := server.Decode[receipts.Result](t, server.CallTool(t, cs, "revert_receipt", map[string]any{"warehouse_id": 1, "items": item}))
	if out.Status != receipts.StatusPreview || out.Lines[0].WarehouseQtyAfter != 1 || puts.Load() != 0 {
		t.Errorf("результат %+v, записів %d", out, puts.Load())
	}
	res := server.CallTool(t, cs, "revert_receipt", map[string]any{"warehouse_id": 1, "items": item, "dry_run": false})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") || puts.Load() != 0 {
		t.Errorf("без confirm: IsError=%v текст=%q записів=%d", res.IsError, server.ResultText(res), puts.Load())
	}
}

func TestReceiveStockRejectsComma(t *testing.T) {
	var puts atomic.Int32
	cs := fakeStock(t, &puts)
	res := server.CallTool(t, cs, "receive_stock", map[string]any{"warehouse_id": 1,
		"items": []any{map[string]any{"sku": "PLA-1,PLA-2", "quantity": 1, "unit_cost": 1}}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "SKU must not contain commas") {
		t.Errorf("IsError=%v текст=%q", res.IsError, server.ResultText(res))
	}
}

func TestReadOnlyHidesReceipts(t *testing.T) {
	cs := server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, http.NotFoundHandler())}, true)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("інструменти read-only: %v", res.Tools)
	}
}
