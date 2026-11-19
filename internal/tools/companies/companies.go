// SPDX-License-Identifier: AGPL-3.0-only

// Package companies — компанії keyCRM: список, перегляд, створення й оновлення.
package companies

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група companies.
var Toolset = server.Toolset{Name: "companies", Register: register}

// defaultInclude — асоціації get_company за замовчуванням: усі документовані.
var defaultInclude = []string{"manager", "buyer", "custom_fields"}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// CompanySummary — компактна проєкція компанії для list_companies.
// Повні дані — через get_company.
type CompanySummary struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	ManagerID int    `json:"manager_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// ListCompaniesInput — вхід list_companies.
type ListCompaniesInput struct {
	listing.Page
	CompanyID      []int  `json:"company_id,omitempty" jsonschema:"ідентифікатори компаній"`
	CreatedBetween string `json:"created_between,omitempty" jsonschema:"період створення (UTC) через кому: 2026-11-01 00:00:00,2026-11-30 23:59:59"`
	UpdatedBetween string `json:"updated_between,omitempty" jsonschema:"період оновлення (UTC) через кому, як created_between"`
}

// GetCompanyInput — вхід get_company.
type GetCompanyInput struct {
	CompanyID int      `json:"company_id" jsonschema:"ідентифікатор компанії в keyCRM"`
	Include   []string `json:"include,omitempty" enum:"manager,buyer,custom_fields" jsonschema:"асоціації; за замовчуванням усі: manager, buyer, custom_fields"`
}

// CustomField — значення користувацького поля.
type CustomField struct {
	UUID  string `json:"uuid" jsonschema:"uuid поля (list_custom_fields), напр. CY_1108"`
	Value any    `json:"value" jsonschema:"значення; для полів-списків — масив рядків"`
}

// CompanyFields — поля компанії, спільні для створення й оновлення.
// Покажчики й omitempty: в оновлення йде лише те, що передано.
type CompanyFields struct {
	Title        *string       `json:"title,omitempty" jsonschema:"повна назва, напр. ТОВ «Приклад»"`
	BankAccount  *string       `json:"bank_account,omitempty" jsonschema:"банківські реквізити, напр. IBAN"`
	Notes        *string       `json:"notes,omitempty" jsonschema:"примітка до компанії"`
	ManagerID    int           `json:"manager_id,omitempty" jsonschema:"відповідальний менеджер (list_users)"`
	BuyerID      []int         `json:"buyer_id,omitempty" jsonschema:"покупці, яких зв'язати з компанією (list_buyers, find_buyer)"`
	CustomFields []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// CreateCompanyInput — вхід create_company.
type CreateCompanyInput struct {
	Name string `json:"name" jsonschema:"назва компанії, обов'язкова"`
	CompanyFields
}

// UpdateCompanyInput — вхід update_company.
type UpdateCompanyInput struct {
	CompanyID int    `json:"company_id" jsonschema:"ідентифікатор компанії"`
	Name      string `json:"name,omitempty" jsonschema:"нова назва"`
	CompanyFields
}

// companyBody — тіло createNewCompany / updateExistingCompany. buyer_id у
// специфікації — масив рядків, тому ідентифікатори перетворюються на рядки.
type companyBody struct {
	Name         string        `json:"name,omitempty"`
	Title        *string       `json:"title,omitempty"`
	BankAccount  *string       `json:"bank_account,omitempty"`
	Notes        *string       `json:"notes,omitempty"`
	ManagerID    int           `json:"manager_id,omitempty"`
	BuyerID      []string      `json:"buyer_id,omitempty"`
	CustomFields []CustomField `json:"custom_fields,omitempty"`
}

func newBody(name string, f CompanyFields) companyBody {
	b := companyBody{
		Name: strings.TrimSpace(name), Title: f.Title, BankAccount: f.BankAccount, Notes: f.Notes,
		ManagerID: f.ManagerID, CustomFields: f.CustomFields,
	}
	for _, id := range f.BuyerID {
		b.BuyerID = append(b.BuyerID, strconv.Itoa(id))
	}
	return b
}

// jsonContent — тип тіла запитів запису.
const jsonContent = "application/json"

// jsonBody серіалізує тіло запиту.
func jsonBody(v any) (io.Reader, []byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}
	return bytes.NewReader(b), b, nil
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_companies",
		Description: "Список компаній keyCRM — компактно: id, назва, повна назва, менеджер, дата створення. Повні дані — get_company. " +
			`Приклади: {"limit": 10}; {"company_id": [112, 113]}; {"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59"}. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfCompanies"},
	}, func(ctx context.Context, in ListCompaniesInput) (listing.Result[CompanySummary], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("company_id", in.CompanyID)
		f.Set("created_between", in.CreatedBetween)
		f.Set("updated_between", in.UpdatedBetween)
		params := &api.GetPaginatedListOfCompaniesParams{Page: &page, Limit: &limit, Filter: f.Param()}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfCompanies(ctx, params))
		if err != nil {
			return listing.Result[CompanySummary]{}, err
		}
		return listing.Decode[CompanySummary](status, body, page, note)
	})

	server.Add(r, server.ToolSpec{
		Name: "get_company",
		Description: "Повна компанія keyCRM за id: реквізити, примітка, менеджер, пов'язані покупці, користувацькі поля. " +
			`Приклади: {"company_id": 112}; {"company_id": 112, "include": ["buyer"]}.`,
		Kind: server.KindRead, OperationIDs: []string{"getCompanyById"},
	}, func(ctx context.Context, in GetCompanyInput) (Object, error) {
		include := in.Include
		if len(include) == 0 {
			include = defaultInclude
		}
		return listing.Object(d.KeyCRM.API.GetCompanyById(ctx, strconv.Itoa(in.CompanyID), &api.GetCompanyByIdParams{Include: listing.Include(include)}))
	})

	server.Add(r, server.ToolSpec{
		Name: "create_company",
		Description: "Створює компанію в keyCRM і за потреби зв'язує з нею покупців. Приклад: " +
			`{"name": "Приклад", "title": "ТОВ «Приклад»", "bank_account": "UA393287040000026006654312944", "buyer_id": [4545]}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewCompany"},
	}, func(ctx context.Context, in CreateCompanyInput) (Object, error) {
		body := newBody(in.Name, in.CompanyFields)
		if body.Name == "" {
			return nil, errors.New("name is required")
		}
		b, _, err := jsonBody(body)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewCompanyWithBody(ctx, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "update_company",
		Description: "Оновлює компанію: назву, реквізити, примітку, менеджера, пов'язаних покупців, користувацькі поля. " +
			`Передавайте лише те, що змінюється. Приклади: {"company_id": 112, "notes": "Оплата за рахунком"}; {"company_id": 112, "buyer_id": [4545, 4546]}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"updateExistingCompany"},
	}, func(ctx context.Context, in UpdateCompanyInput) (Object, error) {
		b, raw, err := jsonBody(newBody(in.Name, in.CompanyFields))
		if err != nil {
			return nil, err
		}
		if string(raw) == "{}" {
			return nil, errors.New("nothing to update: pass at least one field besides company_id")
		}
		return listing.Object(d.KeyCRM.API.UpdateExistingCompanyWithBody(ctx, strconv.Itoa(in.CompanyID), nil, jsonContent, b))
	})
}
