// SPDX-License-Identifier: AGPL-3.0-only

// Package buyers — покупці keyCRM: список, пошук за телефоном чи email,
// перегляд, створення, оновлення й імпорт.
package buyers

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

// Toolset — група buyers.
var Toolset = server.Toolset{Name: "buyers", Register: register}

// maxImport — найбільше покупців в одному import_buyers (ліміт keyCRM).
const maxImport = 50

// defaultInclude — асоціації get_buyer за замовчуванням: усі документовані.
var defaultInclude = []string{"manager", "shipping", "company", "loyalty", "custom_fields"}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// BuyerSummary — компактна проєкція покупця для list_buyers і find_buyer.
// Повні дані — через get_buyer. OrdersCount і OrdersSum — покажчики: поле
// зникає, лише якщо keyCRM його не повернув, а нуль показується.
type BuyerSummary struct {
	ID          int         `json:"id"`
	FullName    string      `json:"full_name"`
	Phone       []string    `json:"phone,omitempty"`
	Email       []string    `json:"email,omitempty"`
	OrdersCount *flexNumber `json:"orders_count,omitempty"`
	OrdersSum   *flexNumber `json:"orders_sum,omitempty"`
	CreatedAt   string      `json:"created_at,omitempty"`
}

// flexNumber приймає число як JSON-число або рядок: keyCRM віддає
// orders_count числом, а orders_sum рядком ("20.00"). Назовні — число.
type flexNumber float64

func (f *flexNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("number %s: %w", b, err)
	}
	*f = flexNumber(v)
	return nil
}

// ListBuyersInput — вхід list_buyers.
type ListBuyersInput struct {
	listing.Page
	BuyerID        []int  `json:"buyer_id,omitempty" jsonschema:"ідентифікатори покупців"`
	CreatedBetween string `json:"created_between,omitempty" jsonschema:"період створення (UTC) через кому: 2026-11-01 00:00:00,2026-11-30 23:59:59"`
	UpdatedBetween string `json:"updated_between,omitempty" jsonschema:"період оновлення (UTC) через кому, як created_between"`
}

// FindBuyerInput — вхід find_buyer.
type FindBuyerInput struct {
	Phone string `json:"phone,omitempty" jsonschema:"телефон у будь-якому поширеному форматі: 067 123 45 67, +38 (067) 123-45-67, 380671234567"`
	Email string `json:"email,omitempty" jsonschema:"email покупця"`
}

// FindBuyerResult — результат find_buyer.
type FindBuyerResult struct {
	Phone   string         `json:"phone,omitempty"`
	Email   string         `json:"email,omitempty"`
	Items   []BuyerSummary `json:"items"`
	HasNext bool           `json:"has_next"`
	Hint    string         `json:"hint"`
}

// GetBuyerInput — вхід get_buyer.
type GetBuyerInput struct {
	BuyerID int      `json:"buyer_id" jsonschema:"ідентифікатор покупця в keyCRM"`
	Include []string `json:"include,omitempty" enum:"manager,shipping,company,loyalty,custom_fields" jsonschema:"асоціації; за замовчуванням усі: manager, shipping, company, loyalty, custom_fields"`
}

// Shipping — адреса доставки покупця.
type Shipping struct {
	Address           string `json:"address,omitempty" jsonschema:"адреса: вулиця, будинок, квартира"`
	AdditionalAddress string `json:"additional_address,omitempty" jsonschema:"додатковий рядок адреси"`
	City              string `json:"city,omitempty" jsonschema:"місто"`
	Region            string `json:"region,omitempty" jsonschema:"область"`
	ZipCode           string `json:"zip_code,omitempty" jsonschema:"поштовий індекс"`
	Country           string `json:"country,omitempty" jsonschema:"країна"`
	RecipientFullName string `json:"recipient_full_name,omitempty" jsonschema:"отримувач, якщо не покупець"`
	RecipientPhone    string `json:"recipient_phone,omitempty" jsonschema:"телефон отримувача в міжнародному форматі, якщо не покупець"`
	WarehouseRef      string `json:"warehouse_ref,omitempty" jsonschema:"UUID відділення Нової Пошти"`
	ShippingService   string `json:"shipping_service,omitempty" jsonschema:"назва служби доставки, напр. Нова Пошта"`
}

// CustomField — значення користувацького поля.
type CustomField struct {
	UUID  string `json:"uuid" jsonschema:"uuid поля (list_custom_fields з model client)"`
	Value any    `json:"value" jsonschema:"значення; для полів-списків — масив рядків"`
}

// BuyerFields — поля покупця, спільні для створення, оновлення й імпорту.
// Покажчики й omitempty: в оновлення йде лише те, що передано.
type BuyerFields struct {
	Birthday     string        `json:"birthday,omitempty" jsonschema:"дата народження, напр. 1990-05-16"`
	Phone        []string      `json:"phone,omitempty" jsonschema:"телефони; зводяться до формату +380671234567. В оновленні передавайте повний перелік"`
	Email        []string      `json:"email,omitempty" jsonschema:"email-и. В оновленні передавайте повний перелік"`
	Note         *string       `json:"note,omitempty" jsonschema:"примітка до покупця"`
	Discount     *float64      `json:"discount,omitempty" jsonschema:"персональна знижка у %"`
	CompanyID    int           `json:"company_id,omitempty" jsonschema:"компанія покупця (list_companies)"`
	ManagerID    int           `json:"manager_id,omitempty" jsonschema:"відповідальний менеджер (list_users)"`
	Shipping     []Shipping    `json:"shipping,omitempty" jsonschema:"адреси доставки"`
	CustomFields []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// NewBuyer — покупець для import_buyers і вхід create_buyer.
type NewBuyer struct {
	FullName string `json:"full_name" jsonschema:"повне ім'я, обов'язкове"`
	BuyerFields
}

// UpdateBuyerInput — вхід update_buyer.
type UpdateBuyerInput struct {
	BuyerID  int    `json:"buyer_id" jsonschema:"ідентифікатор покупця"`
	FullName string `json:"full_name,omitempty" jsonschema:"нове повне ім'я"`
	BuyerFields
}

// ImportBuyersInput — вхід import_buyers.
type ImportBuyersInput struct {
	Buyers  []NewBuyer `json:"buyers" jsonschema:"покупці, до 50 за раз"`
	Confirm bool       `json:"confirm,omitempty" jsonschema:"true — лише після того, як користувач побачив, кого буде створено, і погодився"`
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

// normalizePhones зводить телефони покупця до міжнародного формату, щоб
// find_buyer потім знаходив його за будь-яким написанням номера.
func normalizePhones(phones []string, path string) error {
	for i, p := range phones {
		n, err := NormalizePhone(p)
		if err != nil {
			return fmt.Errorf("%sphone[%d]: %w", path, i, err)
		}
		phones[i] = n
	}
	return nil
}

func validateNew(b *NewBuyer, path string) error {
	b.FullName = strings.TrimSpace(b.FullName)
	if b.FullName == "" {
		return fmt.Errorf("%sfull_name is required", path)
	}
	return normalizePhones(b.Phone, path)
}

// listBuyers запитує сторінку покупців і будує проєкцію.
func listBuyers(ctx context.Context, d server.Deps, page, limit int, note string, f listing.Filter) (listing.Result[BuyerSummary], error) {
	params := &api.GetPaginatedListOfBuyersParams{Page: &page, Limit: &limit, Filter: f.Param()}
	status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfBuyers(ctx, params))
	if err != nil {
		return listing.Result[BuyerSummary]{}, err
	}
	return listing.Decode[BuyerSummary](status, body, page, note)
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_buyers",
		Description: "Список покупців keyCRM — компактно: id, ім'я, телефони, email-и, кількість і сума замовлень, дата створення. Повні дані — get_buyer, " +
			"пошук за телефоном чи email — find_buyer. Приклади: {\"limit\": 10}; {\"buyer_id\": [4545, 4546]}; " +
			`{"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59"}. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfBuyers"},
	}, func(ctx context.Context, in ListBuyersInput) (listing.Result[BuyerSummary], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("buyer_id", in.BuyerID)
		f.Set("created_between", in.CreatedBetween)
		f.Set("updated_between", in.UpdatedBetween)
		return listBuyers(ctx, d, page, limit, note, f)
	})

	server.Add(r, server.ToolSpec{
		Name: "find_buyer",
		Description: "Знаходить покупця за телефоном або email. Телефон можна писати як завгодно — 067 123 45 67, +38 (067) 123-45-67, " +
			"380671234567: він зводиться до +380671234567. Повертає id, ім'я, контакти, orders_count і orders_sum. Замовлення покупця — " +
			"list_orders з buyer_phone (або buyer_email) з результату; їх може бути більше, ніж orders_count: keyCRM, схоже, рахує не всі (напр. без скасованих). " +
			`Приклади: {"phone": "067 123 45 67"}; {"email": "ivan@example.com"}.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfBuyers"},
	}, func(ctx context.Context, in FindBuyerInput) (FindBuyerResult, error) {
		var out FindBuyerResult
		f := listing.Filter{}
		if strings.TrimSpace(in.Phone) != "" {
			p, err := NormalizePhone(in.Phone)
			if err != nil {
				return out, err
			}
			out.Phone = p
			f.Set("buyer_phone", p)
		}
		if e := strings.ToLower(strings.TrimSpace(in.Email)); e != "" {
			if !strings.Contains(e, "@") || strings.Contains(e, ",") {
				return out, fmt.Errorf("email %q: expected one address like ivan@example.com", in.Email)
			}
			out.Email = e
			f.Set("buyer_email", e)
		}
		if len(f) == 0 {
			return out, errors.New("pass phone or email")
		}
		res, err := listBuyers(ctx, d, 1, listing.DefaultLimit, "", f)
		if err != nil {
			return out, err
		}
		out.Items, out.HasNext = res.Items, res.HasNext
		switch {
		case len(out.Items) == 0:
			out.Hint = "buyer not found; try another phone format or email, or list_orders with buyer_phone"
		case out.Phone != "":
			out.Hint = fmt.Sprintf("orders: list_orders with buyer_phone %q; full buyer: get_buyer", out.Phone)
		default:
			out.Hint = fmt.Sprintf("orders: list_orders with buyer_email %q; full buyer: get_buyer", out.Email)
		}
		return out, nil
	})

	server.Add(r, server.ToolSpec{
		Name: "get_buyer",
		Description: "Повний покупець keyCRM за id: телефони, email-и, адреси доставки, компанія, менеджер, програма лояльності, користувацькі поля. " +
			`Приклади: {"buyer_id": 4545}; {"buyer_id": 4545, "include": ["shipping"]}.`,
		Kind: server.KindRead, OperationIDs: []string{"getBuyerById"},
	}, func(ctx context.Context, in GetBuyerInput) (Object, error) {
		include := in.Include
		if len(include) == 0 {
			include = defaultInclude
		}
		return listing.Object(d.KeyCRM.API.GetBuyerById(ctx, in.BuyerID, &api.GetBuyerByIdParams{Include: listing.Include(include)}))
	})

	server.Add(r, server.ToolSpec{
		Name: "create_buyer",
		Description: "Створює покупця в keyCRM. Перед створенням перевірте find_buyer, чи такого ще немає. Телефони зводяться до формату +380671234567. " +
			`Приклад: {"full_name": "Іван Петренко", "phone": ["067 123 45 67"], "email": ["ivan@example.com"]}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewBuyer"},
	}, func(ctx context.Context, in NewBuyer) (Object, error) {
		if err := validateNew(&in, ""); err != nil {
			return nil, err
		}
		b, err := jsonBody(in)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewBuyerWithBody(ctx, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "update_buyer",
		Description: "Оновлює покупця: ім'я, телефони, email-и, примітку, знижку, компанію, менеджера, адреси, користувацькі поля. " +
			"Передавайте лише те, що змінюється; phone і email — повним переліком. " +
			`Приклади: {"buyer_id": 4545, "note": "Оптовик"}; {"buyer_id": 4545, "phone": ["+380671234567", "+380501234567"]}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"updateExistingBuyer"},
	}, func(ctx context.Context, in UpdateBuyerInput) (Object, error) {
		body := struct {
			FullName string `json:"full_name,omitempty"`
			BuyerFields
		}{strings.TrimSpace(in.FullName), in.BuyerFields}
		if b, err := json.Marshal(body); err == nil && string(b) == "{}" {
			return nil, errors.New("nothing to update: pass at least one field besides buyer_id")
		}
		if err := normalizePhones(body.Phone, ""); err != nil {
			return nil, err
		}
		b, err := jsonBody(body)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.UpdateExistingBuyerWithBody(ctx, in.BuyerID, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "import_buyers",
		Description: "Створює до 50 покупців одним запитом (поля кожного — як у create_buyer). Спершу покажіть користувачу перелік; " +
			"викликайте з confirm: true лише після його згоди. Приклад: " +
			`{"buyers": [{"full_name": "Іван Петренко", "phone": ["0671234567"]}], "confirm": true}.`,
		Kind: server.KindWrite, OperationIDs: []string{"importBuyers"},
	}, func(ctx context.Context, in ImportBuyersInput) (Object, error) {
		if err := server.RequireConfirm(in.Confirm, "import_buyers"); err != nil {
			return nil, err
		}
		if len(in.Buyers) == 0 || len(in.Buyers) > maxImport {
			return nil, fmt.Errorf("buyers: pass 1 to %d buyers, got %d", maxImport, len(in.Buyers))
		}
		for i := range in.Buyers {
			if err := validateNew(&in.Buyers[i], fmt.Sprintf("buyers[%d].", i)); err != nil {
				return nil, err
			}
		}
		b, err := jsonBody(map[string]any{"buyers": in.Buyers})
		if err != nil {
			return nil, err
		}
		obj, err := listing.Object(d.KeyCRM.API.ImportBuyersWithBody(ctx, nil, jsonContent, b))
		if err != nil {
			return nil, err
		}
		obj["submitted"] = len(in.Buyers)
		return obj, nil
	})
}
