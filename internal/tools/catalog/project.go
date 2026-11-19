// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// ProductSummary — компактна проєкція товару для list_products.
type ProductSummary struct {
	ID         int     `json:"id"`
	Name       string  `json:"name"`
	SKU        string  `json:"sku,omitempty"`
	Quantity   float64 `json:"quantity"`
	MinPrice   float64 `json:"min_price"`
	MaxPrice   float64 `json:"max_price"`
	UnitType   string  `json:"unit_type,omitempty"`
	CategoryID int     `json:"category_id,omitempty"`
	HasOffers  bool    `json:"has_offers"`
	IsArchived bool    `json:"is_archived"`
}

// rawProduct — поля товару keyCRM, з яких будується ProductSummary.
type rawProduct struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	SKU        *string   `json:"sku"`
	Quantity   flexFloat `json:"quantity"`
	MinPrice   flexFloat `json:"min_price"`
	MaxPrice   flexFloat `json:"max_price"`
	UnitType   *string   `json:"unit_type"`
	CategoryID flexFloat `json:"category_id"`
	HasOffers  bool      `json:"has_offers"`
	IsArchived bool      `json:"is_archived"`
}

func summarizeProduct(p rawProduct) ProductSummary {
	return ProductSummary{
		ID:         p.ID,
		Name:       p.Name,
		SKU:        deref(p.SKU),
		Quantity:   float64(p.Quantity),
		MinPrice:   float64(p.MinPrice),
		MaxPrice:   float64(p.MaxPrice),
		UnitType:   deref(p.UnitType),
		CategoryID: int(p.CategoryID),
		HasOffers:  p.HasOffers,
		IsArchived: p.IsArchived,
	}
}

// OfferSummary — компактна проєкція варіанту для list_offers.
type OfferSummary struct {
	ID             int        `json:"id"`
	ProductID      int        `json:"product_id"`
	ProductName    string     `json:"product_name,omitempty"`
	SKU            string     `json:"sku,omitempty"`
	Barcode        string     `json:"barcode,omitempty"`
	Price          float64    `json:"price"`
	PurchasedPrice float64    `json:"purchased_price"`
	Quantity       float64    `json:"quantity"`
	InReserve      float64    `json:"in_reserve"`
	Properties     []Property `json:"properties,omitempty"`
}

// rawOffer — поля варіанту keyCRM, з яких будується OfferSummary.
type rawOffer struct {
	ID             int        `json:"id"`
	ProductID      int        `json:"product_id"`
	SKU            *string    `json:"sku"`
	Barcode        *string    `json:"barcode"`
	Price          flexFloat  `json:"price"`
	PurchasedPrice flexFloat  `json:"purchased_price"`
	Quantity       flexFloat  `json:"quantity"`
	InReserve      flexFloat  `json:"in_reserve"`
	Properties     []Property `json:"properties"`
	Product        *struct {
		Name string `json:"name"`
	} `json:"product"`
}

func summarizeOffer(o rawOffer) OfferSummary {
	s := OfferSummary{
		ID:             o.ID,
		ProductID:      o.ProductID,
		SKU:            deref(o.SKU),
		Barcode:        deref(o.Barcode),
		Price:          float64(o.Price),
		PurchasedPrice: float64(o.PurchasedPrice),
		Quantity:       float64(o.Quantity),
		InReserve:      float64(o.InReserve),
		Properties:     o.Properties,
	}
	if o.Product != nil {
		s.ProductName = o.Product.Name
	}
	return s
}

// Category — категорія товарів.
type Category struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ParentID *int   `json:"parent_id,omitempty"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// flexFloat приймає число як JSON-число або рядок ("123.50"): числа в keyCRM
// трапляються в обох видах.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("number %s: %w", b, err)
	}
	*f = flexFloat(v)
	return nil
}
