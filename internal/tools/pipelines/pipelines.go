// SPDX-License-Identifier: AGPL-3.0-only

// Package pipelines — воронки keyCRM (ліди, угоди): воронки, їхні статуси,
// картки, оплати й файли карток. Групу можна вимкнути через KEYCRM_TOOLSETS,
// якщо воронками не користуються.
package pipelines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група pipelines.
var Toolset = server.Toolset{Name: "pipelines", Register: register}

// listInclude — асоціації list_pipeline_cards: статус (з pipeline_id і назвою)
// і контакт з покупцем — для проєкції CardSummary.
var listInclude = []string{"contact.client", "status"}

// defaultInclude — асоціації get_pipeline_card за замовчуванням.
var defaultInclude = []string{"contact.client", "products", "manager", "status", "payments"}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// PageInput — вхід list_pipelines.
type PageInput struct {
	listing.Page
}

// StatusesInput — вхід list_pipeline_statuses.
type StatusesInput struct {
	listing.Page
	PipelineID int `json:"pipeline_id" jsonschema:"ідентифікатор воронки (list_pipelines)"`
}

// ListCardsInput — вхід list_pipeline_cards.
type ListCardsInput struct {
	listing.Page
	PipelineID     int    `json:"pipeline_id,omitempty" jsonschema:"ідентифікатор воронки (list_pipelines)"`
	StatusID       []int  `json:"status_id,omitempty" jsonschema:"ідентифікатори статусів (list_pipeline_statuses)"`
	SourceID       int    `json:"source_id,omitempty" jsonschema:"ідентифікатор джерела (list_order_sources)"`
	CreatedBetween string `json:"created_between,omitempty" jsonschema:"період створення (UTC) через кому: 2026-11-01 00:00:00,2026-11-30 23:59:59"`
	UpdatedBetween string `json:"updated_between,omitempty" jsonschema:"період оновлення (UTC) через кому, як created_between"`
}

// GetCardInput — вхід get_pipeline_card.
type GetCardInput struct {
	CardID  int      `json:"card_id" jsonschema:"ідентифікатор картки воронки"`
	Include []string `json:"include,omitempty" enum:"contact.client,products,manager,status,payments,custom_fields" jsonschema:"асоціації; за замовчуванням contact.client, products, manager, status, payments"`
}

// Contact — контакт нової картки. Якщо задано client_id наявного покупця,
// картка створюється на нього, а решта полів ігнорується.
type Contact struct {
	FullName string `json:"full_name,omitempty" jsonschema:"ім'я контакту"`
	Email    string `json:"email,omitempty" jsonschema:"email контакту"`
	Phone    string `json:"phone,omitempty" jsonschema:"телефон у міжнародному форматі, напр. +380671234567"`
	ClientID int    `json:"client_id,omitempty" jsonschema:"ідентифікатор наявного покупця; тоді інші поля контакту ігноруються"`
}

// Product — товар картки (у create_pipeline_card і update_pipeline_card).
type Product struct {
	SKU      string  `json:"sku,omitempty" jsonschema:"артикул; товар з каталогу знаходиться за ним"`
	Name     string  `json:"name,omitempty" jsonschema:"назва (для товару поза каталогом)"`
	Price    float64 `json:"price,omitempty" jsonschema:"ціна за одиницю"`
	Quantity float64 `json:"quantity,omitempty" jsonschema:"кількість, може бути дробовою (0.75 кг)"`
	UnitType string  `json:"unit_type,omitempty" jsonschema:"одиниця виміру, напр. шт або кг (лише при створенні)"`
	Picture  string  `json:"picture,omitempty" jsonschema:"посилання на зображення"`
}

// NewPayment — оплата: у новій картці або add_card_payment.
type NewPayment struct {
	PaymentMethodID int     `json:"payment_method_id,omitempty" jsonschema:"спосіб оплати (list_payment_methods); або payment_method"`
	PaymentMethod   string  `json:"payment_method,omitempty" jsonschema:"назва способу оплати, якщо немає payment_method_id"`
	Amount          float64 `json:"amount" jsonschema:"сума"`
	Status          string  `json:"status,omitempty" enum:"paid,not_paid,canceled" jsonschema:"статус оплати (за замовчуванням paid)"`
	Description     string  `json:"description,omitempty" jsonschema:"коментар до оплати"`
	PaymentDate     string  `json:"payment_date,omitempty" jsonschema:"дата й час (UTC), напр. 2026-11-19 14:00:00; за замовчуванням зараз"`
}

// CustomField — значення користувацького поля картки.
type CustomField struct {
	UUID  string `json:"uuid" jsonschema:"uuid поля (list_custom_fields з model lead), напр. LD_1021"`
	Value any    `json:"value" jsonschema:"значення; для полів-списків — масив рядків"`
}

// CreateCardInput — вхід create_pipeline_card (тіло createNewPipelineCard).
type CreateCardInput struct {
	Title          string        `json:"title,omitempty" jsonschema:"назва картки; без неї — номер картки"`
	PipelineID     int           `json:"pipeline_id,omitempty" jsonschema:"воронка (list_pipelines); без неї — перша воронка"`
	SourceID       int           `json:"source_id,omitempty" jsonschema:"джерело (list_order_sources)"`
	ManagerID      int           `json:"manager_id,omitempty" jsonschema:"відповідальний менеджер (list_users)"`
	ManagerComment string        `json:"manager_comment,omitempty" jsonschema:"коментар менеджера"`
	CommunicateAt  string        `json:"communicate_at,omitempty" jsonschema:"дата й час наступного контакту (UTC), напр. 2026-11-21 14:00:00"`
	Contact        *Contact      `json:"contact" jsonschema:"контакт, обов'язковий: client_id наявного покупця або ім'я/телефон/email"`
	UTMSource      string        `json:"utm_source,omitempty"`
	UTMMedium      string        `json:"utm_medium,omitempty"`
	UTMCampaign    string        `json:"utm_campaign,omitempty"`
	UTMTerm        string        `json:"utm_term,omitempty"`
	UTMContent     string        `json:"utm_content,omitempty"`
	Products       []Product     `json:"products,omitempty" jsonschema:"товари"`
	Payments       []NewPayment  `json:"payments,omitempty" jsonschema:"оплати"`
	CustomFields   []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// UpdateCardFields — тіло updatePipelinesCard.
type UpdateCardFields struct {
	Title         *string       `json:"title,omitempty" jsonschema:"назва картки"`
	Note          *string       `json:"note,omitempty" jsonschema:"примітка до картки"`
	StatusID      int           `json:"status_id,omitempty" jsonschema:"новий статус (list_pipeline_statuses); статус іншої воронки переносить картку туди"`
	SourceID      int           `json:"source_id,omitempty" jsonschema:"джерело (list_order_sources)"`
	ClientID      int           `json:"client_id,omitempty" jsonschema:"зв'язати картку з наявним покупцем"`
	ManagerID     int           `json:"manager_id,omitempty" jsonschema:"відповідальний менеджер (list_users)"`
	CommunicateAt *string       `json:"communicate_at,omitempty" jsonschema:"дата й час наступного контакту (UTC)"`
	Products      []Product     `json:"products,omitempty" jsonschema:"товари для додавання; наявні в картці не оновлюються, завжди додаються нові"`
	CustomFields  []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// UpdateCardInput — вхід update_pipeline_card.
type UpdateCardInput struct {
	CardID int `json:"card_id" jsonschema:"ідентифікатор картки воронки"`
	UpdateCardFields
}

// AddPaymentInput — вхід add_card_payment.
type AddPaymentInput struct {
	CardID int `json:"card_id" jsonschema:"ідентифікатор картки воронки"`
	NewPayment
}

// UpdatePaymentInput — вхід update_card_payment.
type UpdatePaymentInput struct {
	CardID      int     `json:"card_id" jsonschema:"ідентифікатор картки воронки"`
	PaymentID   int     `json:"payment_id" jsonschema:"ідентифікатор оплати (з get_pipeline_card, payments[].id)"`
	Status      string  `json:"status,omitempty" enum:"paid,not_paid,canceled" jsonschema:"новий статус оплати"`
	Description *string `json:"description,omitempty" jsonschema:"коментар до оплати"`
}

// FileInput — вхід attach_card_file і detach_card_file.
type FileInput struct {
	CardID int `json:"card_id" jsonschema:"ідентифікатор картки воронки"`
	FileID int `json:"file_id" jsonschema:"ідентифікатор файлу в сховищі keyCRM"`
}

// jsonContent — тип тіла запитів запису.
const jsonContent = "application/json"

// jsonBody серіалізує тіло запиту.
func jsonBody(v any) (io.Reader, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return bytes.NewReader(b), nil
}

// isEmpty повідомляє, чи тіло не містить жодного поля.
func isEmpty(v any) bool {
	b, err := json.Marshal(v)
	return err == nil && string(b) == "{}"
}

// validatePayments перевіряє, що в кожної оплати задано спосіб.
func validatePayments(ps []NewPayment, path string) error {
	for i, p := range ps {
		if p.PaymentMethodID == 0 && p.PaymentMethod == "" {
			return fmt.Errorf("%s[%d]: set payment_method_id (see list_payment_methods) or payment_method", path, i)
		}
	}
	return nil
}

func id(n int) string { return strconv.Itoa(n) }

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_pipelines",
		Description: "Список воронок keyCRM (ліди, угоди): id, назва, тип. Далі — list_pipeline_statuses для статусів воронки " +
			"і list_pipeline_cards для карток. Якщо воронки вимкнено в налаштуваннях CRM, keyCRM поверне помилку. Приклад: {}.",
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfPipelines"},
	}, func(ctx context.Context, in PageInput) (listing.Result[Item], error) {
		page, limit, note := in.Normalize()
		status, b, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfPipelines(ctx, &api.GetPaginatedListOfPipelinesParams{Page: &page, Limit: &limit}))
		if err != nil {
			return listing.Result[Item]{}, err
		}
		res, err := listing.Decode[Item](status, b, page, note)
		return listing.Project(res, compact), err
	})

	server.Add(r, server.ToolSpec{
		Name: "list_pipeline_statuses",
		Description: "Статуси (етапи) воронки: id, назва, alias, чи фінальний, кількість карток. " +
			"status_id звідси — для list_pipeline_cards і update_pipeline_card. Приклад: {\"pipeline_id\": 1}.",
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfPipelinesStatuses"},
	}, func(ctx context.Context, in StatusesInput) (listing.Result[Item], error) {
		if in.PipelineID <= 0 {
			return listing.Result[Item]{}, errors.New("pipeline_id is required (see list_pipelines)")
		}
		page, limit, note := in.Normalize()
		status, b, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfPipelinesStatuses(ctx, id(in.PipelineID),
			&api.GetPaginatedListOfPipelinesStatusesParams{Page: &page, Limit: &limit}))
		if err != nil {
			return listing.Result[Item]{}, err
		}
		res, err := listing.Decode[Item](status, b, page, note)
		return listing.Project(res, compact), err
	})

	server.Add(r, server.ToolSpec{
		Name: "list_pipeline_cards",
		Description: "Список карток воронок — компактно: id, назва, воронка, статус, джерело, менеджер, контакт, наступний контакт, дата створення. " +
			"Повні дані картки — get_pipeline_card. Фільтри поєднуються. Приклади: " +
			`{"pipeline_id": 1}; {"status_id": [4, 5]}; {"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59"}. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfPipelinesCards"},
	}, func(ctx context.Context, in ListCardsInput) (listing.Result[CardSummary], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("pipeline_id", in.PipelineID)
		f.Set("status_id", in.StatusID)
		f.Set("source_id", in.SourceID)
		f.Set("created_between", in.CreatedBetween)
		f.Set("updated_between", in.UpdatedBetween)
		params := &api.GetPaginatedListOfPipelinesCardsParams{
			Page: &page, Limit: &limit, Filter: f.Param(), Include: listing.Include(listInclude),
		}
		status, b, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfPipelinesCards(ctx, params))
		if err != nil {
			return listing.Result[CardSummary]{}, err
		}
		res, err := listing.Decode[rawCard](status, b, page, note)
		if err != nil {
			return listing.Result[CardSummary]{}, err
		}
		return listing.Project(res, summarize), nil
	})

	server.Add(r, server.ToolSpec{
		Name: "get_pipeline_card",
		Description: "Повна картка воронки за id: контакт і покупець, товари, менеджер, статус, оплати. " +
			`Приклади: {"card_id": 31}; {"card_id": 31, "include": ["custom_fields"]}.`,
		Kind: server.KindRead, OperationIDs: []string{"getPipelinesCard"},
	}, func(ctx context.Context, in GetCardInput) (Object, error) {
		include := in.Include
		if len(include) == 0 {
			include = defaultInclude
		}
		return listing.Object(d.KeyCRM.API.GetPipelinesCard(ctx, id(in.CardID), &api.GetPipelinesCardParams{Include: listing.Include(include)}))
	})

	server.Add(r, server.ToolSpec{
		Name: "create_pipeline_card",
		Description: "Створює картку у воронці (лід). contact обов'язковий: client_id наявного покупця або ім'я/телефон/email. " +
			"pipeline_id — з list_pipelines (без нього — перша воронка). Приклади: " +
			`{"pipeline_id": 1, "title": "Котушка PETG під замовлення", "contact": {"full_name": "Олена Коваль", "phone": "+380671234567"}}; ` +
			`{"contact": {"client_id": 88}, "products": [{"sku": "PETG-BLK-1KG", "price": 650, "quantity": 2}]}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewPipelineCard"},
	}, func(ctx context.Context, in CreateCardInput) (Object, error) {
		if in.Contact == nil || isEmpty(in.Contact) {
			return nil, errors.New("contact is required: set client_id of an existing buyer or full_name/phone/email")
		}
		if err := validatePayments(in.Payments, "payments"); err != nil {
			return nil, err
		}
		b, err := jsonBody(in)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewPipelineCardWithBody(ctx, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "update_pipeline_card",
		Description: "Оновлює картку воронки: назву, примітку, статус (статус іншої воронки переносить картку), джерело, покупця, менеджера, " +
			"дату наступного контакту, додає товари. Передавайте лише те, що змінюється. Приклади: " +
			`{"card_id": 31, "status_id": 5}; {"card_id": 31, "note": "Клієнт просить чорний колір"}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"updatePipelinesCard"},
	}, func(ctx context.Context, in UpdateCardInput) (Object, error) {
		if isEmpty(in.UpdateCardFields) {
			return nil, errors.New("nothing to update: pass at least one field besides card_id")
		}
		b, err := jsonBody(in.UpdateCardFields)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.UpdatePipelinesCardWithBody(ctx, id(in.CardID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "add_card_payment",
		Description: "Додає оплату до картки воронки. Приклад: " +
			`{"card_id": 31, "payment_method_id": 2, "amount": 300, "status": "paid", "description": "Передплата"}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewPipelinesCardPayment"},
	}, func(ctx context.Context, in AddPaymentInput) (Object, error) {
		if err := validatePayments([]NewPayment{in.NewPayment}, "payment"); err != nil {
			return nil, err
		}
		b, err := jsonBody(in.NewPayment)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewPipelinesCardPaymentWithBody(ctx, id(in.CardID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name:        "update_card_payment",
		Description: `Змінює статус або коментар оплати картки воронки. Приклад: {"card_id": 31, "payment_id": 77, "status": "canceled"}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"updateExistingPipelinesCardPayment"},
	}, func(ctx context.Context, in UpdatePaymentInput) (Object, error) {
		body := struct {
			Status      string  `json:"status,omitempty"`
			Description *string `json:"description,omitempty"`
		}{in.Status, in.Description}
		if isEmpty(body) {
			return nil, errors.New("nothing to update: pass status or description")
		}
		b, err := jsonBody(body)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.UpdateExistingPipelinesCardPaymentWithBody(ctx, id(in.CardID), id(in.PaymentID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name:        "attach_card_file",
		Description: `Прикріплює до картки воронки файл, уже завантажений у сховище keyCRM. Приклад: {"card_id": 31, "file_id": 330}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"attachFileToPipelinesCard"},
	}, func(ctx context.Context, in FileInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.AttachFileToPipelinesCard(ctx, id(in.CardID), id(in.FileID), nil))
	})

	server.Add(r, server.ToolSpec{
		Name:        "detach_card_file",
		Description: `Від'єднує файл від картки воронки. Приклад: {"card_id": 31, "file_id": 330}.`,
		Kind:        server.KindDestructive, OperationIDs: []string{"deleteFileFromCard"},
	}, func(ctx context.Context, in FileInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.DeleteFileFromCard(ctx, id(in.CardID), id(in.FileID), nil))
	})
}
