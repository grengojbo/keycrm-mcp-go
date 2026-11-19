// SPDX-License-Identifier: AGPL-3.0-only

// Package catalog — каталог keyCRM: товари, варіанти (offers) і категорії.
package catalog

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

// Toolset — група catalog.
var Toolset = server.Toolset{Name: "catalog", Register: register}

const (
	// maxImport — найбільше товарів в одному import_products (ліміт keyCRM).
	maxImport = 100
	// maxNewOffers — найбільше варіантів в одному create_offers (ліміт keyCRM).
	maxNewOffers = 100
	// maxOfferUpdates — найбільше позицій в одному update_offers (ліміт keyCRM).
	maxOfferUpdates = 10000
	// confirmOffersAbove — update_offers з більшою кількістю позицій потребує confirm.
	confirmOffersAbove = 20
)

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// ListProductsInput — вхід list_products.
type ListProductsInput struct {
	listing.Page
	ProductIDs  []int `json:"product_ids,omitempty" jsonschema:"ідентифікатори товарів"`
	CategoryIDs []int `json:"category_ids,omitempty" jsonschema:"ідентифікатори категорій (list_categories)"`
	IsArchived  *bool `json:"is_archived,omitempty" jsonschema:"true — лише архівні, false — лише активні"`
}

// GetProductInput — вхід get_product.
type GetProductInput struct {
	ProductID int      `json:"product_id" jsonschema:"ідентифікатор товару в keyCRM"`
	Include   []string `json:"include,omitempty" enum:"custom_fields" jsonschema:"асоціації: custom_fields — користувацькі поля"`
}

// CustomField — значення користувацького поля товару.
type CustomField struct {
	UUID  string `json:"uuid" jsonschema:"uuid поля (list_custom_fields з model crm_product), напр. CT_1108"`
	Value any    `json:"value" jsonschema:"значення; для полів-списків — масив рядків"`
}

// NewProduct — тіло нового товару (createNewProduct, елемент createNewProductImport).
type NewProduct struct {
	Name           string        `json:"name" jsonschema:"назва товару, обов'язкова"`
	Description    string        `json:"description,omitempty" jsonschema:"опис"`
	Pictures       []string      `json:"pictures,omitempty" jsonschema:"посилання на зображення, до 6, перше — головне"`
	CurrencyCode   string        `json:"currency_code,omitempty" jsonschema:"валюта, напр. UAH"`
	SKU            string        `json:"sku,omitempty" jsonschema:"артикул"`
	Barcode        string        `json:"barcode,omitempty" jsonschema:"штрих-код"`
	Price          float64       `json:"price,omitempty" jsonschema:"ціна за одиницю"`
	PurchasedPrice float64       `json:"purchased_price,omitempty" jsonschema:"закупівельна ціна (собівартість) за одиницю"`
	UnitType       string        `json:"unit_type,omitempty" jsonschema:"одиниця виміру, напр. шт або кг"`
	Weight         float64       `json:"weight,omitempty" jsonschema:"вага за замовчуванням"`
	Length         float64       `json:"length,omitempty" jsonschema:"довжина за замовчуванням"`
	Width          float64       `json:"width,omitempty" jsonschema:"ширина за замовчуванням"`
	Height         float64       `json:"height,omitempty" jsonschema:"висота за замовчуванням"`
	CategoryID     int           `json:"category_id,omitempty" jsonschema:"категорія (list_categories)"`
	CustomFields   []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// CreateProductInput — вхід create_product.
type CreateProductInput struct {
	NewProduct
}

// ProductUpdate — тіло updateExistingProduct; передаються лише змінювані поля.
type ProductUpdate struct {
	Name           string        `json:"name,omitempty" jsonschema:"назва"`
	Description    *string       `json:"description,omitempty" jsonschema:"опис"`
	Pictures       []string      `json:"pictures,omitempty" jsonschema:"посилання на зображення, до 6, перше — головне"`
	CurrencyCode   string        `json:"currency_code,omitempty" jsonschema:"валюта, напр. UAH"`
	SKU            string        `json:"sku,omitempty" jsonschema:"артикул"`
	Barcode        *string       `json:"barcode,omitempty" jsonschema:"штрих-код"`
	Price          *float64      `json:"price,omitempty" jsonschema:"ціна за одиницю"`
	PurchasedPrice *float64      `json:"purchased_price,omitempty" jsonschema:"закупівельна ціна (собівартість) за одиницю"`
	UnitType       string        `json:"unit_type,omitempty" jsonschema:"одиниця виміру, напр. шт або кг"`
	Weight         *float64      `json:"weight,omitempty" jsonschema:"вага за замовчуванням"`
	Length         *float64      `json:"length,omitempty" jsonschema:"довжина за замовчуванням"`
	Width          *float64      `json:"width,omitempty" jsonschema:"ширина за замовчуванням"`
	Height         *float64      `json:"height,omitempty" jsonschema:"висота за замовчуванням"`
	CategoryID     int           `json:"category_id,omitempty" jsonschema:"категорія (list_categories)"`
	CustomFields   []CustomField `json:"custom_fields,omitempty" jsonschema:"користувацькі поля"`
}

// UpdateProductInput — вхід update_product.
type UpdateProductInput struct {
	ProductID int `json:"product_id" jsonschema:"ідентифікатор товару"`
	ProductUpdate
}

// ImportProductsInput — вхід import_products.
type ImportProductsInput struct {
	Products []NewProduct `json:"products" jsonschema:"товари, до 100 за раз"`
	Confirm  bool         `json:"confirm,omitempty" jsonschema:"true — лише після того, як користувач побачив, що буде створено, і погодився"`
}

// Property — властивість варіанту.
type Property struct {
	Name  string `json:"name" jsonschema:"назва властивості, напр. Колір"`
	Value string `json:"value" jsonschema:"значення, напр. Чорний"`
}

// NewOffer — новий варіант товару.
type NewOffer struct {
	SKU            string     `json:"sku,omitempty" jsonschema:"артикул варіанту"`
	Barcode        string     `json:"barcode,omitempty" jsonschema:"штрих-код"`
	Price          float64    `json:"price,omitempty" jsonschema:"ціна"`
	PurchasedPrice float64    `json:"purchased_price,omitempty" jsonschema:"закупівельна ціна (собівартість)"`
	Weight         float64    `json:"weight,omitempty" jsonschema:"вага"`
	Length         float64    `json:"length,omitempty" jsonschema:"довжина"`
	Width          float64    `json:"width,omitempty" jsonschema:"ширина"`
	Height         float64    `json:"height,omitempty" jsonschema:"висота"`
	ImageURL       string     `json:"image_url,omitempty" jsonschema:"посилання на зображення варіанту"`
	Properties     []Property `json:"properties,omitempty" jsonschema:"властивості варіанту, напр. колір і розмір"`
}

// CreateOffersInput — вхід create_offers.
type CreateOffersInput struct {
	ProductID int        `json:"product_id" jsonschema:"ідентифікатор товару"`
	Offers    []NewOffer `json:"offers" jsonschema:"варіанти, до 100 за раз"`
}

// ListOffersInput — вхід list_offers.
type ListOffersInput struct {
	listing.Page
	IDs        []int    `json:"ids,omitempty" jsonschema:"ідентифікатори варіантів"`
	ProductIDs []int    `json:"product_ids,omitempty" jsonschema:"ідентифікатори товарів"`
	SKUs       []string `json:"skus,omitempty" jsonschema:"артикули варіантів (без ком)"`
	IsArchived *bool    `json:"is_archived,omitempty" jsonschema:"true — лише архівні, false — лише активні"`
	Sort       string   `json:"sort,omitempty" enum:"id,-id" jsonschema:"сортування: id — від старих, -id — від нових"`
}

// OfferUpdate — позиція update_offers: рівно одне з id чи sku плюс поля, що змінюються.
type OfferUpdate struct {
	ID             int      `json:"id,omitempty" jsonschema:"ідентифікатор варіанту; або sku"`
	SKU            string   `json:"sku,omitempty" jsonschema:"артикул варіанту; або id"`
	Price          *float64 `json:"price,omitempty" jsonschema:"нова ціна"`
	PurchasedPrice *float64 `json:"purchased_price,omitempty" jsonschema:"нова закупівельна ціна (собівартість)"`
	Weight         *float64 `json:"weight,omitempty" jsonschema:"вага"`
	Length         *float64 `json:"length,omitempty" jsonschema:"довжина"`
	Width          *float64 `json:"width,omitempty" jsonschema:"ширина"`
	Height         *float64 `json:"height,omitempty" jsonschema:"висота"`
	ImageURL       string   `json:"image_url,omitempty" jsonschema:"посилання на зображення варіанту"`
}

// UpdateOffersInput — вхід update_offers.
type UpdateOffersInput struct {
	Offers  []OfferUpdate `json:"offers" jsonschema:"варіанти для зміни"`
	Confirm bool          `json:"confirm,omitempty" jsonschema:"обов'язкове true, якщо позицій понад 20, — лише після того, як користувач побачив зміни й погодився"`
}

// ListCategoriesInput — вхід list_categories.
type ListCategoriesInput struct {
	listing.Page
	CategoryIDs []int `json:"category_ids,omitempty" jsonschema:"ідентифікатори категорій"`
	ParentIDs   []int `json:"parent_ids,omitempty" jsonschema:"ідентифікатори батьківських категорій"`
}

// CreateCategoryInput — вхід create_category.
type CreateCategoryInput struct {
	Name     string `json:"name" jsonschema:"назва категорії"`
	ParentID int    `json:"parent_id,omitempty" jsonschema:"батьківська категорія, якщо створюєте підкатегорію"`
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

func id(n int) string { return strconv.Itoa(n) }

// validateNewProduct перевіряє обов'язкову назву товару.
func validateNewProduct(p NewProduct, path string) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%sname is required", path)
	}
	return nil
}

// validateSKUFilter відхиляє артикули з комою: кома в keyCRM розділяє значення
// фільтра, тож такий артикул тихо перетворився б на два.
func validateSKUFilter(skus []string) error {
	for _, s := range skus {
		if strings.Contains(s, ",") {
			return fmt.Errorf("skus: %q: SKU must not contain commas", s)
		}
	}
	return nil
}

// validateOfferUpdates перевіряє, що кожна позиція має рівно одне з id/sku
// і хоча б одне поле для зміни.
func validateOfferUpdates(offers []OfferUpdate) error {
	if len(offers) == 0 || len(offers) > maxOfferUpdates {
		return fmt.Errorf("offers: pass 1 to %d offers, got %d", maxOfferUpdates, len(offers))
	}
	for i, o := range offers {
		hasSKU := strings.TrimSpace(o.SKU) != ""
		if (o.ID > 0) == hasSKU {
			return fmt.Errorf("offers[%d]: set exactly one of id or sku", i)
		}
		changes := o
		changes.ID, changes.SKU = 0, ""
		if isEmpty(changes) {
			return fmt.Errorf("offers[%d]: nothing to update: pass price, purchased_price, dimensions or image_url", i)
		}
	}
	return nil
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_products",
		Description: "Список товарів каталогу keyCRM — компактно: id, назва, артикул, залишок, ціни, категорія, чи є варіанти. " +
			"Повний товар — get_product, варіанти з цінами й собівартістю — list_offers. Приклади: " +
			`{"category_ids": [4]}; {"is_archived": false, "limit": 50}; {"product_ids": [101, 102]}.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfProducts"},
	}, func(ctx context.Context, in ListProductsInput) (listing.Result[ProductSummary], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("product_id", in.ProductIDs)
		f.Set("category_id", in.CategoryIDs)
		f.Set("is_archived", in.IsArchived)
		params := &api.GetPaginatedListOfProductsParams{Page: &page, Limit: &limit, Filter: f.Param()}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfProducts(ctx, params))
		if err != nil {
			return listing.Result[ProductSummary]{}, err
		}
		res, err := listing.Decode[rawProduct](status, body, page, note)
		if err != nil {
			return listing.Result[ProductSummary]{}, err
		}
		return listing.Project(res, summarizeProduct), nil
	})

	server.Add(r, server.ToolSpec{
		Name:        "get_product",
		Description: `Повний товар keyCRM за id. Приклади: {"product_id": 101}; {"product_id": 101, "include": ["custom_fields"]}.`,
		Kind:        server.KindRead, OperationIDs: []string{"getProductById"},
	}, func(ctx context.Context, in GetProductInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.GetProductById(ctx, id(in.ProductID), &api.GetProductByIdParams{Include: listing.Include(in.Include)}))
	})

	server.Add(r, server.ToolSpec{
		Name: "create_product",
		Description: "Створює товар без варіантів (варіанти додаються через create_offers). Приклад: " +
			`{"name": "PLA пластик 1 кг чорний", "sku": "PLA-BLK-1KG", "price": 650, "purchased_price": 420, "unit_type": "шт", "category_id": 4}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewProduct"},
	}, func(ctx context.Context, in CreateProductInput) (Object, error) {
		if err := validateNewProduct(in.NewProduct, ""); err != nil {
			return nil, err
		}
		b, err := jsonBody(in.NewProduct)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewProductWithBody(ctx, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "update_product",
		Description: "Оновлює товар: назву, опис, ціни, габарити, категорію, користувацькі поля. Передавайте лише те, що змінюється. " +
			"Ціни й собівартість окремих варіантів змінює update_offers. Приклад: " +
			`{"product_id": 101, "price": 690, "description": "Новий опис"}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"updateExistingProduct"},
	}, func(ctx context.Context, in UpdateProductInput) (Object, error) {
		if isEmpty(in.ProductUpdate) {
			return nil, errors.New("nothing to update: pass at least one field besides product_id")
		}
		b, err := jsonBody(in.ProductUpdate)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.UpdateExistingProductWithBody(ctx, id(in.ProductID), nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "import_products",
		Description: "Створює до 100 товарів одним запитом (поля кожного — як у create_product). Спершу покажіть користувачу перелік; " +
			"викликайте з confirm: true лише після його згоди. Приклад: " +
			`{"products": [{"name": "PETG 1 кг білий", "sku": "PETG-WHT-1KG", "price": 700}], "confirm": true}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewProductImport"},
	}, func(ctx context.Context, in ImportProductsInput) (Object, error) {
		if err := server.RequireConfirm(in.Confirm, "import_products"); err != nil {
			return nil, err
		}
		if len(in.Products) == 0 || len(in.Products) > maxImport {
			return nil, fmt.Errorf("products: pass 1 to %d products, got %d", maxImport, len(in.Products))
		}
		for i, p := range in.Products {
			if err := validateNewProduct(p, fmt.Sprintf("products[%d].", i)); err != nil {
				return nil, err
			}
		}
		b, err := jsonBody(map[string]any{"products": in.Products})
		if err != nil {
			return nil, err
		}
		obj, err := listing.Object(d.KeyCRM.API.CreateNewProductImportWithBody(ctx, nil, jsonContent, b))
		if err != nil {
			return nil, err
		}
		obj["submitted"] = len(in.Products)
		return obj, nil
	})

	server.Add(r, server.ToolSpec{
		Name: "create_offers",
		Description: "Додає до 100 варіантів до товару (напр. кольори чи розміри). Приклад: " +
			`{"product_id": 101, "offers": [{"sku": "PLA-RED-1KG", "price": 650, "purchased_price": 420, ` +
			`"properties": [{"name": "Колір", "value": "Червоний"}]}]}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createNewProductOffers"},
	}, func(ctx context.Context, in CreateOffersInput) (Object, error) {
		if len(in.Offers) == 0 || len(in.Offers) > maxNewOffers {
			return nil, fmt.Errorf("offers: pass 1 to %d offers, got %d", maxNewOffers, len(in.Offers))
		}
		b, err := jsonBody(map[string]any{"offers": in.Offers})
		if err != nil {
			return nil, err
		}
		obj, err := listing.Object(d.KeyCRM.API.CreateNewProductOffersWithBody(ctx, id(in.ProductID), nil, jsonContent, b))
		if err != nil {
			return nil, err
		}
		obj["submitted"] = len(in.Offers)
		return obj, nil
	})

	server.Add(r, server.ToolSpec{
		Name: "list_offers",
		Description: "Список варіантів товарів — компактно: id, товар, артикул, ціна, закупівельна ціна (собівартість), залишок, резерв, властивості. " +
			"Залишки по складах — get_stock. Приклади: " +
			`{"skus": ["PLA-BLK-1KG", "PETG-WHT-1KG"]}; {"product_ids": [101]}; {"sort": "-id", "limit": 10}.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfOffers"},
	}, func(ctx context.Context, in ListOffersInput) (listing.Result[OfferSummary], error) {
		if err := validateSKUFilter(in.SKUs); err != nil {
			return listing.Result[OfferSummary]{}, err
		}
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("id", in.IDs)
		f.Set("product_id", in.ProductIDs)
		f.Set("sku", in.SKUs)
		f.Set("is_archived", in.IsArchived)
		params := &api.GetPaginatedListOfOffersParams{
			Page: &page, Limit: &limit, Filter: f.Param(),
			Include: listing.Include([]string{"product"}),
		}
		if in.Sort != "" {
			params.Sort = &in.Sort
		}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfOffers(ctx, params))
		if err != nil {
			return listing.Result[OfferSummary]{}, err
		}
		res, err := listing.Decode[rawOffer](status, body, page, note)
		if err != nil {
			return listing.Result[OfferSummary]{}, err
		}
		return listing.Project(res, summarizeOffer), nil
	})

	server.Add(r, server.ToolSpec{
		Name: "update_offers",
		Description: "Змінює ціну, закупівельну ціну (собівартість), габарити чи зображення варіантів. Кожна позиція — рівно одне з id чи sku. " +
			"Понад 20 позицій — спершу покажіть користувачу перелік змін і викликайте з confirm: true лише після його згоди. " +
			"Для надходження товару зі зміною собівартості використовуйте receive_stock. Приклад: " +
			`{"offers": [{"sku": "PLA-BLK-1KG", "price": 690}, {"id": 22, "purchased_price": 430}]}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"offersUpdate"},
	}, func(ctx context.Context, in UpdateOffersInput) (Object, error) {
		if len(in.Offers) > confirmOffersAbove {
			if err := server.RequireConfirm(in.Confirm, fmt.Sprintf("update_offers with more than %d offers", confirmOffersAbove)); err != nil {
				return nil, err
			}
		}
		if err := validateOfferUpdates(in.Offers); err != nil {
			return nil, err
		}
		b, err := jsonBody(map[string]any{"offers": in.Offers})
		if err != nil {
			return nil, err
		}
		obj, err := listing.Object(d.KeyCRM.API.OffersUpdateWithBody(ctx, jsonContent, b))
		if err != nil {
			return nil, err
		}
		obj["submitted"] = len(in.Offers)
		return obj, nil
	})

	server.Add(r, server.ToolSpec{
		Name:        "list_categories",
		Description: `Категорії товарів: id, назва, батьківська категорія. Приклади: {}; {"parent_ids": [4]} — підкатегорії категорії 4.`,
		Kind:        server.KindRead, OperationIDs: []string{"getPaginatedListOfCategories"},
	}, func(ctx context.Context, in ListCategoriesInput) (listing.Result[Category], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("category_id", in.CategoryIDs)
		f.Set("parent_id", in.ParentIDs)
		params := &api.GetPaginatedListOfCategoriesParams{Page: &page, Limit: &limit, Filter: f.Param()}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfCategories(ctx, params))
		if err != nil {
			return listing.Result[Category]{}, err
		}
		return listing.Decode[Category](status, body, page, note)
	})

	server.Add(r, server.ToolSpec{
		Name:        "create_category",
		Description: `Створює категорію товарів. Приклади: {"name": "Пластик"}; {"name": "PLA", "parent_id": 4} — підкатегорія.`,
		Kind:        server.KindWrite, OperationIDs: []string{"createNewCategory"},
	}, func(ctx context.Context, in CreateCategoryInput) (Object, error) {
		if strings.TrimSpace(in.Name) == "" {
			return nil, errors.New("name is required")
		}
		b, err := jsonBody(in)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateNewCategoryWithBody(ctx, nil, jsonContent, b))
	})
}
