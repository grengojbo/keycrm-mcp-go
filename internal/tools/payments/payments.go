// SPDX-License-Identifier: AGPL-3.0-only

// Package payments — зовнішні транзакції keyCRM (виписки банків і платіжних
// сервісів): список і прив'язка транзакції до оплати замовлення.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група payments.
var Toolset = server.Toolset{Name: "payments", Register: register}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// ListTransactionsInput — вхід list_external_transactions.
type ListTransactionsInput struct {
	listing.Page
	ID              []int    `json:"id,omitempty" jsonschema:"ідентифікатори транзакцій у keyCRM"`
	TransactionUUID []string `json:"transaction_uuid,omitempty" jsonschema:"зовнішні ідентифікатори транзакцій (з банку чи платіжного сервісу)"`
	GatewayID       []int    `json:"gateway_id,omitempty" jsonschema:"ідентифікатори платіжних сервісів"`
	IsAttached      *bool    `json:"is_attached,omitempty" jsonschema:"true — лише прив'язані до оплат, false — лише неприв'язані"`
	CreatedBetween  string   `json:"created_between,omitempty" jsonschema:"період створення (UTC) через кому: 2026-11-01 00:00:00,2026-11-30 23:59:59"`
}

// AttachTransactionInput — вхід attach_external_transaction.
type AttachTransactionInput struct {
	PaymentID       int    `json:"payment_id" jsonschema:"ідентифікатор оплати замовлення (get_order, payments[].id)"`
	TransactionID   int    `json:"transaction_id,omitempty" jsonschema:"ідентифікатор транзакції в keyCRM (list_external_transactions, id); або transaction_uuid"`
	TransactionUUID string `json:"transaction_uuid,omitempty" jsonschema:"зовнішній ідентифікатор транзакції, якщо немає transaction_id"`
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_external_transactions",
		Description: "Список зовнішніх транзакцій keyCRM — надходження з банків і платіжних сервісів: сума, валюта, дата, опис, прив'язані оплати. " +
			"Допомагає знайти платіж клієнта й прив'язати його до оплати замовлення (attach_external_transaction). Приклади: " +
			`{"is_attached": false} — неприв'язані; {"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59", "limit": 50}. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfExternalTransactions"},
	}, func(ctx context.Context, in ListTransactionsInput) (listing.Result[Object], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("id", in.ID)
		f.Set("transaction_uuid", in.TransactionUUID)
		f.Set("gateway_id", in.GatewayID)
		f.Set("is_attached", in.IsAttached)
		f.Set("created_between", in.CreatedBetween)
		params := &api.GetPaginatedListOfExternalTransactionsParams{Page: &page, Limit: &limit, Filter: f.Param()}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfExternalTransactions(ctx, params))
		if err != nil {
			return listing.Result[Object]{}, err
		}
		return listing.Decode[Object](status, body, page, note)
	})

	server.Add(r, server.ToolSpec{
		Name: "attach_external_transaction",
		Description: "Прив'язує зовнішню транзакцію до оплати замовлення. Спершу знайдіть транзакцію (list_external_transactions) і оплату (get_order, payments[].id). " +
			`Приклади: {"payment_id": 77, "transaction_id": 22}; {"payment_id": 77, "transaction_uuid": "NlpssJSQedtUoB9i"}.`,
		Kind: server.KindWrite, OperationIDs: []string{"attachExternalTransactionToPayment"},
	}, func(ctx context.Context, in AttachTransactionInput) (Object, error) {
		if in.TransactionID == 0 && in.TransactionUUID == "" {
			return nil, errors.New("set transaction_id or transaction_uuid (see list_external_transactions)")
		}
		body := struct {
			TransactionID   int    `json:"transaction_id,omitempty"`
			TransactionUUID string `json:"transaction_uuid,omitempty"`
		}{in.TransactionID, in.TransactionUUID}
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		return listing.Object(d.KeyCRM.API.AttachExternalTransactionToPaymentWithBody(ctx, strconv.Itoa(in.PaymentID), "application/json", bytes.NewReader(b)))
	})
}
