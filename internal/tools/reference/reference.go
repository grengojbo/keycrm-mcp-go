// SPDX-License-Identifier: AGPL-3.0-only

// Package reference — довідники keyCRM: статуси, джерела, теги, способи оплати,
// типи витрат, служби доставки, статуси товарів, користувацькі поля, користувачі.
// Звідси модель бере ідентифікатори для фільтрів і інструментів запису.
package reference

import (
	"context"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група reference.
var Toolset = server.Toolset{Name: "reference", Register: register}

// Item — елемент довідника: поля keyCRM без службових дат (див. compact).
type Item = map[string]any

// compact прибирає з елемента довідника службові дати (*_at: created_at,
// updated_at, deleted_at, last_logged_at) і порожні значення, щоб не
// витрачати контекст моделі. Ідентифікатори, назви, alias і прапорці лишаються.
func compact(it Item) Item {
	for k, v := range it {
		if v == nil || strings.HasSuffix(k, "_at") {
			delete(it, k)
		}
	}
	return it
}

// PageInput — вхід довідників без фільтрів.
type PageInput struct {
	listing.Page
}

// UsersInput — вхід list_users.
type UsersInput struct {
	listing.Page
	Status   []string `json:"status,omitempty" enum:"active,pending,blocked" jsonschema:"статус користувача; для призначення менеджера беріть active"`
	FullName string   `json:"full_name,omitempty" jsonschema:"ім'я користувача, напр. Олена Коваль"`
	Email    string   `json:"email,omitempty" jsonschema:"email користувача"`
	RoleID   []int    `json:"role_id,omitempty" jsonschema:"ідентифікатори ролей"`
	ID       []int    `json:"id,omitempty" jsonschema:"ідентифікатори користувачів"`
}

// CustomFieldsInput — вхід list_custom_fields.
type CustomFieldsInput struct {
	Model          []string `json:"model,omitempty" enum:"lead,order,client,crm_product" jsonschema:"сутності, поля яких потрібні: lead (картка воронки), order (замовлення), client (покупець), crm_product (товар)"`
	IncludeOptions bool     `json:"include_options,omitempty" jsonschema:"додати варіанти для полів-списків (select)"`
}

// CustomFieldsResult — результат list_custom_fields (не пагінований).
type CustomFieldsResult struct {
	Items []Item `json:"items"`
}

// pageCall викликає пагіновану операцію довідника й повертає статус і тіло.
type pageCall func(ctx context.Context, c *api.ClientWithResponses, page, limit int) (int, []byte, error)

// simpleLists — довідники без фільтрів: назва, operationId, опис, виклик.
var simpleLists = []struct {
	name, opID, desc string
	call             pageCall
}{
	{
		"list_order_statuses", "getPaginatedListOfStatuses",
		"Статуси замовлень keyCRM (id, назва, група). id використовуйте як status_id у list_orders і update_order. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfStatuses(ctx, &api.GetPaginatedListOfStatusesParams{Page: &p, Limit: &l}))
		},
	},
	{
		"list_order_sources", "getPaginatedListOfSources",
		"Джерела замовлень (сайт, Instagram, Rozetka…). id — це source_id для list_orders і create_order. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfSources(ctx, &api.GetPaginatedListOfSourcesParams{Page: &p, Limit: &l}))
		},
	},
	{
		"list_order_tags", "getPaginatedListOfTags",
		"Теги замовлень. id — це tag_id для add_order_tag і remove_order_tag. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfTags(ctx, &api.GetPaginatedListOfTagsParams{Page: &p, Limit: &l}))
		},
	},
	{
		"list_payment_methods", "getPaginatedListOfPaymentMethods",
		"Способи оплати. id — це payment_method_id для add_order_payment. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfPaymentMethods(ctx, &api.GetPaginatedListOfPaymentMethodsParams{Page: &p, Limit: &l}))
		},
	},
	{
		"list_expense_types", "getPaginatedListOfExpenseTypes",
		"Типи витрат (пакування, комісія…). id — це expense_type_id для add_order_expense. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfExpenseTypes(ctx, &api.GetPaginatedListOfExpenseTypesParams{Page: &p, Limit: &l}))
		},
	},
	{
		"list_delivery_services", "getPaginatedListOfDeliveryServices",
		"Служби доставки (Нова Пошта, Укрпошта…). id — це shipping.delivery_service_id у create_order і update_order. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfDeliveryServices(ctx, &api.GetPaginatedListOfDeliveryServicesParams{Page: &p, Limit: &l}))
		},
	},
	{
		"list_product_statuses", "getPaginatedListOfProductStatuses",
		"Статуси товарів у замовленні. id — це products[].product_status_id в update_order. Приклад: {\"limit\": 50}.",
		func(ctx context.Context, c *api.ClientWithResponses, p, l int) (int, []byte, error) {
			return listing.Raw(c.GetPaginatedListOfProductStatuses(ctx, &api.GetPaginatedListOfProductStatusesParams{Page: &p, Limit: &l}))
		},
	},
}

func register(r *server.Registry, d server.Deps) {
	for _, l := range simpleLists {
		server.Add(r, server.ToolSpec{
			Name: l.name, Description: l.desc, Kind: server.KindRead, OperationIDs: []string{l.opID},
		}, func(ctx context.Context, in PageInput) (listing.Result[Item], error) {
			page, limit, note := in.Normalize()
			status, b, err := l.call(ctx, d.KeyCRM.API, page, limit)
			if err != nil {
				return listing.Result[Item]{}, err
			}
			res, err := listing.Decode[Item](status, b, page, note)
			return listing.Project(res, compact), err
		})
	}

	server.Add(r, server.ToolSpec{
		Name: "list_users",
		Description: "Користувачі (співробітники) keyCRM. id — це manager_id для create_order. " +
			"Приклади: {\"status\": [\"active\"]}, {\"full_name\": \"Олена\"}.",
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfUsers"},
	}, func(ctx context.Context, in UsersInput) (listing.Result[Item], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("status", in.Status)
		f.Set("full_name", in.FullName)
		f.Set("email", in.Email)
		f.Set("role_id", in.RoleID)
		f.Set("id", in.ID)
		status, b, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfUsers(ctx, &api.GetPaginatedListOfUsersParams{
			Page: &page, Limit: &limit, Filter: f.Param(),
		}))
		if err != nil {
			return listing.Result[Item]{}, err
		}
		out, err := listing.Decode[Item](status, b, page, note)
		return listing.Project(out, compact), err
	})

	server.Add(r, server.ToolSpec{
		Name: "list_custom_fields",
		Description: "Користувацькі поля keyCRM (uuid, назва, тип, сутність). uuid використовуйте в custom_fields при створенні й оновленні замовлень. " +
			"Приклад: {\"model\": [\"order\"], \"include_options\": true}.",
		Kind: server.KindRead, OperationIDs: []string{"getListOfCustomFields"},
	}, func(ctx context.Context, in CustomFieldsInput) (CustomFieldsResult, error) {
		f := listing.Filter{}
		f.Set("model", in.Model)
		params := &api.GetListOfCustomFieldsParams{Filter: f.Param()}
		if in.IncludeOptions {
			params.Include = listing.Include([]string{"options"})
		}
		obj, err := listing.Object(d.KeyCRM.API.GetListOfCustomFields(ctx, params))
		if err != nil {
			return CustomFieldsResult{}, err
		}
		return CustomFieldsResult{Items: items(obj)}, nil
	})
}

// items дістає масив об'єктів з відповіді: сам масив ({"data": [...]} після
// DecodeObject) або поле data пагінованої відповіді.
func items(obj map[string]any) []Item {
	out := []Item{}
	arr, _ := obj["data"].([]any)
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			out = append(out, compact(m))
		}
	}
	return out
}
