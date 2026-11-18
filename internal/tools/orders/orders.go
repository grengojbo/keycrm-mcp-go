// SPDX-License-Identifier: AGPL-3.0-only

// Package orders — замовлення keyCRM: список, перегляд, створення, оновлення,
// імпорт, оплати, витрати, теги й файли.
package orders

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

// Toolset — група orders.
var Toolset = server.Toolset{Name: "orders", Register: register}

// maxImport — найбільше замовлень в одному import_orders (ліміт keyCRM).
const maxImport = 50

// defaultInclude — асоціації get_order за замовчуванням: покупець, статус,
// товари (з shipment_type: склад чи виробництво), оплати, доставка, теги.
var defaultInclude = []string{"buyer", "status", "products.offer", "payments", "shipping.deliveryService", "tags"}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// ListOrdersInput — вхід list_orders.
type ListOrdersInput struct {
	listing.Page
	StatusID        int      `json:"status_id,omitempty" jsonschema:"ідентифікатор статусу (list_order_statuses)"`
	SourceID        int      `json:"source_id,omitempty" jsonschema:"ідентифікатор джерела (list_order_sources)"`
	SourceUUID      string   `json:"source_uuid,omitempty" jsonschema:"номер замовлення в джерелі, напр. номер з сайту"`
	BuyerPhone      string   `json:"buyer_phone,omitempty" jsonschema:"телефон покупця в міжнародному форматі, напр. +380671234567"`
	BuyerEmail      string   `json:"buyer_email,omitempty" jsonschema:"email покупця"`
	CreatedBetween  string   `json:"created_between,omitempty" jsonschema:"період створення (UTC) через кому: 2026-11-01 00:00:00,2026-11-30 23:59:59"`
	UpdatedBetween  string   `json:"updated_between,omitempty" jsonschema:"період оновлення (UTC) через кому, як created_between"`
	ShippingBetween string   `json:"shipping_between,omitempty" jsonschema:"період доставки (UTC) через кому, як created_between"`
	PaymentStatus   []string `json:"payment_status,omitempty" enum:"paid,overpaid,part_paid,not_paid" jsonschema:"статуси оплати"`
	HasTrackingCode *bool    `json:"has_tracking_code,omitempty" jsonschema:"true — лише з трек-номером, false — лише без"`
	Sort            string   `json:"sort,omitempty" enum:"id,-id" jsonschema:"сортування: id — від старих, -id — від нових"`
}

// GetOrderInput — вхід get_order.
type GetOrderInput struct {
	OrderID int      `json:"order_id" jsonschema:"ідентифікатор замовлення в keyCRM"`
	Include []string `json:"include,omitempty" enum:"buyer,products.offer,manager,tags,status,marketing,payments,shipping.lastHistory,shipping.deliveryService,expenses,custom_fields,assigned" jsonschema:"асоціації; за замовчуванням buyer, status, products.offer, payments, shipping.deliveryService, tags"`
}

// Buyer — покупець нового замовлення.
type Buyer struct {
	FullName string `json:"full_name,omitempty" jsonschema:"ім'я покупця"`
	Email    string `json:"email,omitempty" jsonschema:"email покупця"`
	Phone    string `json:"phone,omitempty" jsonschema:"телефон у міжнародному форматі, напр. +380671234567"`
}

// ShippingBase — поля доставки, спільні для створення й оновлення.
type ShippingBase struct {
	DeliveryServiceID      int    `json:"delivery_service_id,omitempty" jsonschema:"служба доставки (list_delivery_services)"`
	TrackingCode           string `json:"tracking_code,omitempty" jsonschema:"трек-номер"`
	ShippingAddressCity    string `json:"shipping_address_city,omitempty" jsonschema:"місто"`
	ShippingAddressCountry string `json:"shipping_address_country,omitempty" jsonschema:"країна"`
	ShippingAddressRegion  string `json:"shipping_address_region,omitempty" jsonschema:"область"`
	ShippingAddressZip     string `json:"shipping_address_zip,omitempty" jsonschema:"поштовий індекс"`
	ShippingSecondaryLine  string `json:"shipping_secondary_line,omitempty" jsonschema:"вулиця, будинок, квартира"`
	ShippingReceivePoint   string `json:"shipping_receive_point,omitempty" jsonschema:"точка видачі, напр. Відділення №12"`
	RecipientFullName      string `json:"recipient_full_name,omitempty" jsonschema:"отримувач, якщо не покупець"`
	RecipientPhone         string `json:"recipient_phone,omitempty" jsonschema:"телефон отримувача, якщо не покупець"`
	WarehouseRef           string `json:"warehouse_ref,omitempty" jsonschema:"UUID відділення Нової Пошти (разом з delivery_service_id НП)"`
	ShippingDate           string `json:"shipping_date,omitempty" jsonschema:"дата відправки, напр. 2026-11-20"`
}

// Shipping — доставка нового замовлення.
type Shipping struct {
	ShippingBase
	ShippingService string `json:"shipping_service,omitempty" jsonschema:"назва служби доставки, якщо немає delivery_service_id"`
}

// Marketing — UTM-мітки.
type Marketing struct {
	UTMSource   string `json:"utm_source,omitempty"`
	UTMMedium   string `json:"utm_medium,omitempty"`
	UTMCampaign string `json:"utm_campaign,omitempty"`
	UTMTerm     string `json:"utm_term,omitempty"`
	UTMContent  string `json:"utm_content,omitempty"`
}

// Property — атрибут товару.
type Property struct {
	Name  string `json:"name" jsonschema:"назва атрибута, напр. Колір"`
	Value string `json:"value" jsonschema:"значення, напр. Чорний"`
}

// NewProduct — товар нового замовлення.
type NewProduct struct {
	SKU             string     `json:"sku,omitempty" jsonschema:"артикул; товар з каталогу знаходиться за ним"`
	Name            string     `json:"name,omitempty" jsonschema:"назва (для товару поза каталогом)"`
	Price           float64    `json:"price,omitempty" jsonschema:"ціна за одиницю"`
	PurchasedPrice  float64    `json:"purchased_price,omitempty" jsonschema:"закупівельна ціна за одиницю"`
	Quantity        float64    `json:"quantity,omitempty" jsonschema:"кількість, може бути дробовою (0.75 кг)"`
	UnitType        string     `json:"unit_type,omitempty" jsonschema:"одиниця виміру, напр. шт або кг"`
	DiscountPercent float64    `json:"discount_percent,omitempty" jsonschema:"знижка на товар у %"`
	DiscountAmount  float64    `json:"discount_amount,omitempty" jsonschema:"фіксована знижка на товар"`
	Comment         string     `json:"comment,omitempty" jsonschema:"коментар до товару"`
	Picture         string     `json:"picture,omitempty" jsonschema:"посилання на зображення"`
	Properties      []Property `json:"properties,omitempty" jsonschema:"атрибути товару"`
}

// NewPayment — оплата: у новому замовленні або add_order_payment.
type NewPayment struct {
	PaymentMethodID int     `json:"payment_method_id,omitempty" jsonschema:"спосіб оплати (list_payment_methods); або payment_method"`
	PaymentMethod   string  `json:"payment_method,omitempty" jsonschema:"назва способу оплати, якщо немає payment_method_id"`
	Amount          float64 `json:"amount" jsonschema:"сума"`
	Status          string  `json:"status,omitempty" enum:"paid,not_paid,canceled" jsonschema:"статус оплати (за замовчуванням paid)"`
	Description     string  `json:"description,omitempty" jsonschema:"коментар до оплати"`
	PaymentDate     string  `json:"payment_date,omitempty" jsonschema:"дата й час (UTC), напр. 2026-11-19 14:00:00; за замовчуванням зараз"`
}

// CustomField — значення користувацького поля.
type CustomField struct {
	UUID  string `json:"uuid" jsonschema:"uuid поля (list_custom_fields), напр. OR_1037"`
	Value any    `json:"value" jsonschema:"значення; для полів-списків — масив рядків"`
}

// OrderFields — тіло нового замовлення (createNewOrder, елемент createNewOrderImport).
type OrderFields struct {
	SourceID        int           `json:"source_id" jsonschema:"джерело (list_order_sources), обов'язкове"`
	SourceUUID      string        `json:"source_uuid,omitempty" jsonschema:"номер замовлення в джерелі"`
	Buyer           Buyer         `json:"buyer" jsonschema:"покупець"`
	ManagerID       int           `json:"manager_id,omitempty" jsonschema:"відповідальний менеджер (list_users)"`
	BuyerComment    string        `json:"buyer_comment,omitempty" jsonschema:"коментар покупця"`
	ManagerComment  string        `json:"manager_comment,omitempty" jsonschema:"коментар менеджера"`
	Promocode       string        `json:"promocode,omitempty"`
	DiscountPercent float64       `json:"discount_percent,omitempty" jsonschema:"знижка на замовлення у %"`
	DiscountAmount  float64       `json:"discount_amount,omitempty" jsonschema:"фіксована знижка на замовлення"`
	ShippingPrice   float64       `json:"shipping_price,omitempty" jsonschema:"вартість доставки"`
	OrderedAt       string        `json:"ordered_at,omitempty" jsonschema:"дата замовлення (UTC), напр. 2026-11-19 14:44:00"`
	Shipping        *Shipping     `json:"shipping,omitempty" jsonschema:"доставка"`
	Marketing       *Marketing    `json:"marketing,omitempty" jsonschema:"UTM-мітки"`
	Products        []NewProduct  `json:"products,omitempty" jsonschema:"товари"`
	Payments        []NewPayment  `json:"payments,omitempty" jsonschema:"оплати"`
	CustomFields    []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// CreateOrderInput — вхід create_order.
type CreateOrderInput struct {
	OrderFields
	Confirm bool `json:"confirm,omitempty" jsonschema:"true — лише після того, як користувач побачив, що буде створено, і погодився"`
}

// ImportOrdersInput — вхід import_orders.
type ImportOrdersInput struct {
	Orders  []OrderFields `json:"orders" jsonschema:"замовлення, до 50 за раз"`
	Confirm bool          `json:"confirm,omitempty" jsonschema:"true — лише після того, як користувач побачив, що буде створено, і погодився"`
}

// UpdateProduct — товар в update_order: знаходиться за id позиції або sku;
// якщо не знайдено — додається новий.
type UpdateProduct struct {
	ID              int      `json:"id,omitempty" jsonschema:"id позиції в замовленні (не id з каталогу)"`
	SKU             string   `json:"sku,omitempty" jsonschema:"артикул для пошуку позиції"`
	Name            string   `json:"name,omitempty" jsonschema:"назва (оновлюється лише для товарів поза каталогом)"`
	Comment         string   `json:"comment,omitempty" jsonschema:"коментар (замінює попередній)"`
	Price           *float64 `json:"price,omitempty" jsonschema:"ціна за одиницю"`
	PurchasedPrice  *float64 `json:"purchased_price,omitempty" jsonschema:"закупівельна ціна за одиницю"`
	Quantity        *float64 `json:"quantity,omitempty" jsonschema:"кількість"`
	DiscountPercent *float64 `json:"discount_percent,omitempty" jsonschema:"знижка у %"`
	DiscountAmount  *float64 `json:"discount_amount,omitempty" jsonschema:"фіксована знижка"`
	ProductStatusID int      `json:"product_status_id,omitempty" jsonschema:"статус товару (list_product_statuses)"`
}

// UpdateOrderFields — тіло updateExistingOrder.
type UpdateOrderFields struct {
	StatusID        int             `json:"status_id,omitempty" jsonschema:"новий статус (list_order_statuses)"`
	BuyerComment    *string         `json:"buyer_comment,omitempty" jsonschema:"коментар покупця"`
	ManagerComment  *string         `json:"manager_comment,omitempty" jsonschema:"коментар менеджера"`
	DiscountPercent *float64        `json:"discount_percent,omitempty" jsonschema:"знижка на замовлення у %"`
	DiscountAmount  *float64        `json:"discount_amount,omitempty" jsonschema:"фіксована знижка на замовлення"`
	Shipping        *ShippingBase   `json:"shipping,omitempty" jsonschema:"доставка; трек-номер, що вже є, повторно додати не можна"`
	Products        []UpdateProduct `json:"products,omitempty" jsonschema:"товари для оновлення або додавання"`
	CustomFields    []CustomField   `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// UpdateOrderInput — вхід update_order.
type UpdateOrderInput struct {
	OrderID int `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	UpdateOrderFields
}

// AddPaymentInput — вхід add_order_payment.
type AddPaymentInput struct {
	OrderID int `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	NewPayment
}

// UpdatePaymentInput — вхід update_order_payment.
type UpdatePaymentInput struct {
	OrderID     int     `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	PaymentID   int     `json:"payment_id" jsonschema:"ідентифікатор оплати (з get_order, payments[].id)"`
	Status      string  `json:"status,omitempty" enum:"paid,not_paid,canceled" jsonschema:"новий статус оплати"`
	Description *string `json:"description,omitempty" jsonschema:"коментар до оплати"`
}

// AddExpenseInput — вхід add_order_expense.
type AddExpenseInput struct {
	OrderID       int     `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	ExpenseTypeID int     `json:"expense_type_id,omitempty" jsonschema:"тип витрати (list_expense_types); або expense_type"`
	ExpenseType   string  `json:"expense_type,omitempty" jsonschema:"назва типу витрати, якщо немає expense_type_id"`
	Amount        float64 `json:"amount" jsonschema:"сума"`
	Description   string  `json:"description,omitempty" jsonschema:"коментар"`
	PaymentDate   string  `json:"payment_date,omitempty" jsonschema:"дата й час (UTC); за замовчуванням зараз"`
}

// UpdateExpenseInput — вхід update_order_expense.
type UpdateExpenseInput struct {
	OrderID     int     `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	ExpenseID   int     `json:"expense_id" jsonschema:"ідентифікатор витрати (з get_order з include expenses)"`
	Status      string  `json:"status,omitempty" enum:"canceled" jsonschema:"canceled — скасувати витрату"`
	Description *string `json:"description,omitempty" jsonschema:"коментар"`
}

// TagInput — вхід add_order_tag і remove_order_tag.
type TagInput struct {
	OrderID int `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	TagID   int `json:"tag_id" jsonschema:"ідентифікатор тегу (list_order_tags)"`
}

// FileInput — вхід attach_order_file і detach_order_file.
type FileInput struct {
	OrderID int `json:"order_id" jsonschema:"ідентифікатор замовлення"`
	FileID  int `json:"file_id" jsonschema:"ідентифікатор файлу в сховищі keyCRM"`
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

// isEmpty повідомляє, чи тіло оновлення не містить жодного поля.
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

func validateOrder(o OrderFields, path string) error {
	if o.SourceID <= 0 {
		return fmt.Errorf("%ssource_id is required (see list_order_sources)", path)
	}
	return validatePayments(o.Payments, path+"payments")
}

func id(n int) string { return strconv.Itoa(n) }

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_orders",
		Description: "Список замовлень keyCRM — компактно: id, номер у джерелі, статус, статус оплати, сума, покупець, дата створення. " +
			"Повні дані замовлення — get_order. Фільтри поєднуються. Приклади: " +
			`{"status_id": 3}; {"buyer_phone": "+380671234567"}; ` +
			`{"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59", "payment_status": ["not_paid","part_paid"]}; ` +
			`{"sort": "-id", "limit": 10} — останні 10. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfOrders"},
	}, func(ctx context.Context, in ListOrdersInput) (listing.Result[OrderSummary], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("status_id", in.StatusID)
		f.Set("source_id", in.SourceID)
		f.Set("source_uuid", in.SourceUUID)
		f.Set("buyer_phone", in.BuyerPhone)
		f.Set("buyer_email", in.BuyerEmail)
		f.Set("created_between", in.CreatedBetween)
		f.Set("updated_between", in.UpdatedBetween)
		f.Set("shipping_between", in.ShippingBetween)
		f.Set("payment_status", in.PaymentStatus)
		f.Set("has_tracking_code", in.HasTrackingCode)
		params := &api.GetPaginatedListOfOrdersParams{
			Page: &page, Limit: &limit, Filter: f.Param(),
			Include: listing.Include([]string{"buyer", "status"}),
		}
		if in.Sort != "" {
			params.Sort = &in.Sort
		}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfOrders(ctx, params))
		if err != nil {
			return listing.Result[OrderSummary]{}, err
		}
		res, err := listing.Decode[rawOrder](status, body, page, note)
		if err != nil {
			return listing.Result[OrderSummary]{}, err
		}
		return listing.Project(res, summarize), nil
	})

	server.Add(r, server.ToolSpec{
		Name: "get_order",
		Description: "Повне замовлення keyCRM за id: покупець, товари (shipment_type: warehouse — зі складу, manufacturing — під виробництво), " +
			"оплати, доставка, теги. Приклади: {\"order_id\": 1024}; {\"order_id\": 1024, \"include\": [\"expenses\", \"custom_fields\"]}.",
		Kind: server.KindRead, OperationIDs: []string{"getOrderById"},
	}, func(ctx context.Context, in GetOrderInput) (Object, error) {
		include := in.Include
		if len(include) == 0 {
			include = defaultInclude
		}
		return listing.Object(d.KeyCRM.API.GetOrderById(ctx, in.OrderID, &api.GetOrderByIdParams{Include: listing.Include(include)}))
	})

	server.Add(r, server.ToolSpec{
		Name: "create_order",
		Description: "Створює замовлення в keyCRM. Спершу покажіть користувачу джерело, покупця, товари й суми; викликайте з confirm: true лише після його згоди. " +
			"source_id — з list_order_sources, payment_method_id — з list_payment_methods. Приклад: " +
			`{"source_id": 1, "buyer": {"full_name": "Іван Петренко", "phone": "+380671234567"}, ` +
			`"products": [{"sku": "PLA-BLK-1KG", "price": 650, "quantity": 2}], "confirm": true}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewOrder"},
	}, func(ctx context.Context, in CreateOrderInput) (Object, error) {
		if err := server.RequireConfirm(in.Confirm, "create_order"); err != nil {
			return nil, err
		}
		if err := validateOrder(in.OrderFields, ""); err != nil {
			return nil, err
		}
		b, err := jsonBody(in.OrderFields)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewOrderWithBody(ctx, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "update_order",
		Description: "Оновлює замовлення: статус, коментарі, знижку, доставку, товари, користувацькі поля. Передавайте лише те, що змінюється. " +
			"Товар шукається за id позиції в замовленні або sku; не знайдений — додається. Приклади: " +
			`{"order_id": 1024, "status_id": 5}; {"order_id": 1024, "shipping": {"tracking_code": "20450000000000"}}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"updateExistingOrder"},
	}, func(ctx context.Context, in UpdateOrderInput) (Object, error) {
		if isEmpty(in.UpdateOrderFields) {
			return nil, errors.New("nothing to update: pass at least one field besides order_id")
		}
		b, err := jsonBody(in.UpdateOrderFields)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.UpdateExistingOrderWithBody(ctx, in.OrderID, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "import_orders",
		Description: "Створює до 50 замовлень одним запитом (поля кожного — як у create_order). Спершу покажіть користувачу перелік; " +
			"викликайте з confirm: true лише після його згоди. Приклад: " +
			`{"orders": [{"source_id": 1, "source_uuid": "W-1001", "buyer": {"full_name": "Іван"}}], "confirm": true}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewOrderImport"},
	}, func(ctx context.Context, in ImportOrdersInput) (Object, error) {
		if err := server.RequireConfirm(in.Confirm, "import_orders"); err != nil {
			return nil, err
		}
		if len(in.Orders) == 0 || len(in.Orders) > maxImport {
			return nil, fmt.Errorf("orders: pass 1 to %d orders, got %d", maxImport, len(in.Orders))
		}
		for i, o := range in.Orders {
			if err := validateOrder(o, fmt.Sprintf("orders[%d].", i)); err != nil {
				return nil, err
			}
		}
		b, err := jsonBody(map[string]any{"orders": in.Orders})
		if err != nil {
			return nil, err
		}
		obj, err := listing.Object(d.KeyCRM.API.CreateNewOrderImportWithBody(ctx, nil, jsonContent, b))
		if err != nil {
			return nil, err
		}
		obj["submitted"] = len(in.Orders)
		return obj, nil
	})

	server.Add(r, server.ToolSpec{
		Name: "add_order_payment",
		Description: "Додає оплату до замовлення. Приклад: " +
			`{"order_id": 1024, "payment_method_id": 2, "amount": 1300, "status": "paid", "description": "Передплата"}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewOrderPayment"},
	}, func(ctx context.Context, in AddPaymentInput) (Object, error) {
		if err := validatePayments([]NewPayment{in.NewPayment}, "payment"); err != nil {
			return nil, err
		}
		b, err := jsonBody(in.NewPayment)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewOrderPaymentWithBody(ctx, id(in.OrderID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name:        "update_order_payment",
		Description: `Змінює статус або коментар оплати замовлення. Приклад: {"order_id": 1024, "payment_id": 77, "status": "canceled"}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"updateExistingOrderPayment"},
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
		return listing.Object(d.KeyCRM.API.UpdateExistingOrderPaymentWithBody(ctx, id(in.OrderID), id(in.PaymentID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "add_order_expense",
		Description: "Додає витрату до замовлення (пакування, комісія, доставка за рахунок магазину). Приклад: " +
			`{"order_id": 1024, "expense_type_id": 3, "amount": 45.5, "description": "Пакування"}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewOrderExpense"},
	}, func(ctx context.Context, in AddExpenseInput) (Object, error) {
		if in.ExpenseTypeID == 0 && in.ExpenseType == "" {
			return nil, errors.New("set expense_type_id (see list_expense_types) or expense_type")
		}
		body := struct {
			ExpenseTypeID int     `json:"expense_type_id,omitempty"`
			ExpenseType   string  `json:"expense_type,omitempty"`
			Amount        float64 `json:"amount"`
			Description   string  `json:"description,omitempty"`
			PaymentDate   string  `json:"payment_date,omitempty"`
		}{in.ExpenseTypeID, in.ExpenseType, in.Amount, in.Description, in.PaymentDate}
		b, err := jsonBody(body)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewOrderExpenseWithBody(ctx, id(in.OrderID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name:        "update_order_expense",
		Description: `Скасовує витрату замовлення або змінює її коментар. Приклад: {"order_id": 1024, "expense_id": 15, "status": "canceled"}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"updateExistingOrderExpense"},
	}, func(ctx context.Context, in UpdateExpenseInput) (Object, error) {
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
		return listing.Object(d.KeyCRM.API.UpdateExistingOrderExpenseWithBody(ctx, id(in.OrderID), id(in.ExpenseID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name:        "add_order_tag",
		Description: `Додає тег до замовлення (tag_id — з list_order_tags). Приклад: {"order_id": 1024, "tag_id": 4}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"attachTagToOrder"},
	}, func(ctx context.Context, in TagInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.AttachTagToOrder(ctx, id(in.OrderID), id(in.TagID), nil))
	})

	server.Add(r, server.ToolSpec{
		Name:        "remove_order_tag",
		Description: `Знімає тег із замовлення. Приклад: {"order_id": 1024, "tag_id": 4}.`,
		Kind:        server.KindDestructive, OperationIDs: []string{"deleteTagFromOrder"},
	}, func(ctx context.Context, in TagInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.DeleteTagFromOrder(ctx, id(in.OrderID), id(in.TagID), nil))
	})

	server.Add(r, server.ToolSpec{
		Name:        "attach_order_file",
		Description: `Прикріплює до замовлення файл, уже завантажений у сховище keyCRM. Приклад: {"order_id": 1024, "file_id": 330}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"attachFileToOrder"},
	}, func(ctx context.Context, in FileInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.AttachFileToOrder(ctx, id(in.OrderID), id(in.FileID), nil))
	})

	server.Add(r, server.ToolSpec{
		Name:        "detach_order_file",
		Description: `Від'єднує файл від замовлення. Приклад: {"order_id": 1024, "file_id": 330}.`,
		Kind:        server.KindDestructive, OperationIDs: []string{"deleteFileFromOrder"},
	}, func(ctx context.Context, in FileInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.DeleteFileFromOrder(ctx, id(in.OrderID), id(in.FileID), nil))
	})
}
