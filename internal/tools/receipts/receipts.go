// SPDX-License-Identifier: AGPL-3.0-only

// Package receipts — інструменти прибуткових накладних: надходження товару
// на склад із середньозваженою собівартістю та відкат надходження.
package receipts

import (
	"context"

	"github.com/grengojbo/keycrm-mcp-go/internal/receipts"
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
)

// Toolset — група receipts.
var Toolset = server.Toolset{Name: "receipts", Register: register}

// ops — операції keyCRM, які викликають обидва інструменти.
var ops = []string{"getPaginatedListOfStocks", "offersStocksUpdate", "offersUpdate"}

// Input — вхід receive_stock і revert_receipt.
type Input struct {
	receipts.Receipt
	DryRun  *bool `json:"dry_run,omitempty" jsonschema:"true (за замовчуванням) — лише показати «було → стане», нічого не змінюючи"`
	Confirm bool  `json:"confirm,omitempty" jsonschema:"обов'язкове true при dry_run: false — лише після того, як користувач побачив попередній перегляд і погодився"`
}

// dryRun — dry_run з урахуванням значення за замовчуванням true.
func (in Input) dryRun() bool { return in.DryRun == nil || *in.DryRun }

func register(r *server.Registry, d server.Deps) {
	svc := receipts.NewService(d.KeyCRM)

	server.Add(r, server.ToolSpec{
		Name: "receive_stock",
		Description: "Прибуткова накладна: додає товар на склад і перераховує закупівельну ціну (собівартість) як середньозважену: " +
			"(загальний залишок × стара ціна + кількість × ціна надходження) / (залишок + кількість), до копійок; якщо залишку немає — ціна надходження. " +
			"За замовчуванням dry_run: true — повертає таблицю «було → стане» і нічого не змінює. Покажіть її користувачу; " +
			"лише після його згоди викличте ту саму накладну з dry_run: false і confirm: true. Невідомий артикул відхиляє всю накладну. " +
			"Статус partial означає, що залишки записано, а собівартість ні: не повторюйте накладну, а виставте ціни з warnings через update_offers. " +
			"Постачальник, номер і дата документа лише повертаються в результаті — keyCRM їх не зберігає. Приклад: " +
			`{"warehouse_id": 1, "items": [{"sku": "PLA-BLK-1KG", "quantity": 10, "unit_cost": 420}, {"offer_id": 23, "quantity": 0.75, "unit_cost": 800}], ` +
			`"supplier": "ТОВ Пластик", "doc_number": "ПН-17", "doc_date": "2026-11-19"}.`,
		Kind: server.KindWrite, OperationIDs: ops,
	}, func(ctx context.Context, in Input) (receipts.Result, error) {
		if in.dryRun() {
			return svc.Preview(ctx, in.Receipt)
		}
		if err := server.RequireConfirm(in.Confirm, "receive_stock"); err != nil {
			return receipts.Result{}, err
		}
		return svc.Apply(ctx, in.Receipt)
	})

	server.Add(r, server.ToolSpec{
		Name: "revert_receipt",
		Description: "Відкат прибуткової накладної: передайте ту саму накладну (склад, позиції з кількістю й ціною), що й у receive_stock. " +
			"Віднімає кількості зі складу й повертає собівартість зворотною формулою (залишок × ціна − кількість × ціна накладної) / (залишок − кількість). " +
			"Якщо на складі менше, ніж відкочується, — відмова, нічого не записано. Якщо залишок стає нулем, собівартість не змінюється (є попередження). " +
			"Якщо між накладною й відкатом були інші надходження чи списання, результат наближений. " +
			"За замовчуванням dry_run: true — лише «було → стане»; запис — з dry_run: false і confirm: true після згоди користувача. Приклад: " +
			`{"warehouse_id": 1, "items": [{"sku": "PLA-BLK-1KG", "quantity": 10, "unit_cost": 420}]}.`,
		Kind: server.KindWrite, OperationIDs: ops,
	}, func(ctx context.Context, in Input) (receipts.Result, error) {
		if !in.dryRun() {
			if err := server.RequireConfirm(in.Confirm, "revert_receipt"); err != nil {
				return receipts.Result{}, err
			}
		}
		return svc.Revert(ctx, in.Receipt, in.dryRun())
	})
}
