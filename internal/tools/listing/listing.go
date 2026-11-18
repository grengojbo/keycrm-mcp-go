// SPDX-License-Identifier: AGPL-3.0-only

// Package listing — спільне для інструментів: пагінація, фільтри keyCRM
// (filter[поле]=a,b) і розбір відповідей API.
package listing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// DefaultLimit — розмір сторінки, якщо limit не задано.
const DefaultLimit = 25

// Page — параметри пагінації на вході list_* інструментів. Вбудовується в
// структуру входу, тож поля page і limit опиняються на верхньому рівні.
// Поле сторінки зветься Number, а не Page: виведення JSON Schema губить
// поле, що має ту саму назву, що й вбудований тип.
type Page struct {
	Number int `json:"page,omitempty" jsonschema:"номер сторінки, з 1 (за замовчуванням 1)"`
	Limit  int `json:"limit,omitempty" jsonschema:"елементів на сторінці, 1–50 (за замовчуванням 25)"`
}

// Normalize повертає сторінку й розмір сторінки з урахуванням значень за
// замовчуванням і ліміту keyCRM; note пояснює, якщо limit було зменшено.
func (p Page) Normalize() (page, limit int, note string) {
	page, limit = p.Number, p.Limit
	if page < 1 {
		page = 1
	}
	switch {
	case limit < 1:
		limit = DefaultLimit
	case limit > keycrm.MaxPageSize:
		limit = keycrm.MaxPageSize
		note = fmt.Sprintf("limit capped at %d", keycrm.MaxPageSize)
	}
	return page, limit, note
}

// Result — сторінка результатів list_* інструмента.
type Result[T any] struct {
	Items   []T    `json:"items"`
	Page    int    `json:"page"`
	HasNext bool   `json:"has_next"`
	Total   int    `json:"total,omitempty"`
	Note    string `json:"note,omitempty"`
}

// maxResponseBytes — найбільше тіло відповіді keyCRM, яке читає інструмент.
const maxResponseBytes = 32 << 20

// Raw читає й закриває тіло відповіді згенерованого клієнта і повертає статус
// і тіло, щоб передати їх у Decode чи DecodeObject. Використовуйте методи без
// суфікса WithResponse: ті розбирають тіло в згенеровані типи й падають, щойно
// реальна відповідь keyCRM розходиться зі специфікацією (напр. products[].picture
// приходить об'єктом, а не рядком):
//
//	status, body, err := listing.Raw(c.API.GetOrderById(ctx, id, params))
func Raw(resp *http.Response, err error) (int, []byte, error) {
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, fmt.Errorf("read keyCRM response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return 0, nil, fmt.Errorf("keyCRM response exceeds %d MB", maxResponseBytes>>20)
	}
	return resp.StatusCode, body, nil
}

// Decode перевіряє статус відповіді keyCRM і розбирає пагіновану відповідь
// ({data, total, next_page_url}) у Result. page і note переносяться в результат.
func Decode[T any](status int, body []byte, page int, note string) (Result[T], error) {
	if err := keycrm.CheckResponse(status, body); err != nil {
		return Result[T]{}, err
	}
	var env struct {
		Data        []T     `json:"data"`
		Total       int     `json:"total"`
		CurrentPage int     `json:"current_page"`
		NextPageURL *string `json:"next_page_url"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return Result[T]{}, fmt.Errorf("decode keyCRM list response: %w", err)
	}
	r := Result[T]{
		Items:   env.Data,
		Page:    page,
		HasNext: env.NextPageURL != nil && *env.NextPageURL != "",
		Total:   env.Total,
		Note:    note,
	}
	if env.CurrentPage > 0 {
		r.Page = env.CurrentPage
	}
	if r.Items == nil {
		r.Items = []T{}
	}
	return r, nil
}

// Project перетворює елементи сторінки на компактну проєкцію.
func Project[T, P any](r Result[T], f func(T) P) Result[P] {
	out := Result[P]{Items: make([]P, 0, len(r.Items)), Page: r.Page, HasNext: r.HasNext, Total: r.Total, Note: r.Note}
	for _, it := range r.Items {
		out.Items = append(out.Items, f(it))
	}
	return out
}

// DecodeObject перевіряє статус відповіді keyCRM і повертає тіло як об'єкт.
// Порожнє тіло (204) — {"ok": true}; тіло-масив чи значення — {"data": …}.
func DecodeObject(status int, body []byte) (map[string]any, error) {
	if err := keycrm.CheckResponse(status, body); err != nil {
		return nil, err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return map[string]any{"ok": true}, nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("decode keyCRM response: %w", err)
	}
	if obj, ok := v.(map[string]any); ok {
		return obj, nil
	}
	return map[string]any{"data": v}, nil
}

// Object — Raw і DecodeObject разом, для виклику в один рядок:
//
//	return listing.Object(c.API.AttachTagToOrder(ctx, orderID, tagID, nil))
func Object(resp *http.Response, err error) (map[string]any, error) {
	status, body, err := Raw(resp, err)
	if err != nil {
		return nil, err
	}
	return DecodeObject(status, body)
}

// Filter — фільтри keyCRM, що передаються як filter[поле]=значення.
type Filter map[string]any

// Set додає фільтр, якщо значення не порожнє. Підтримує string, int, bool,
// *bool, []string і []int (інші типи — через fmt.Sprint); кілька значень з'єднуються комою, як чекає keyCRM.
// Рядки обрізаються від пробілів.
func (f Filter) Set(key string, v any) {
	var s string
	switch t := v.(type) {
	case string:
		s = strings.TrimSpace(t)
	case int:
		if t != 0 {
			s = strconv.Itoa(t)
		}
	case bool:
		if t {
			s = "true"
		}
	case *bool:
		if t != nil {
			s = strconv.FormatBool(*t)
		}
	case []string:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		s = strings.Join(parts, ",")
	case []int:
		parts := make([]string, 0, len(t))
		for _, n := range t {
			parts = append(parts, strconv.Itoa(n))
		}
		s = strings.Join(parts, ",")
	default:
		s = strings.TrimSpace(fmt.Sprint(t))
	}
	if s != "" {
		f[key] = s
	}
}

// Param повертає фільтри для параметра filter згенерованого клієнта або nil,
// якщо фільтрів немає.
func (f Filter) Param() *map[string]any {
	if len(f) == 0 {
		return nil
	}
	m := map[string]any(f)
	return &m
}

// Include з'єднує асоціації для параметра include; порожній список — nil.
func Include(values []string) *string {
	if len(values) == 0 {
		return nil
	}
	s := strings.Join(values, ",")
	return &s
}
