// SPDX-License-Identifier: AGPL-3.0-only

package orders

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// OrderSummary — компактна проєкція замовлення для list_orders. Повні дані —
// через get_order. Ім'я покупця тут не маскується: це вихід для моделі, а не лог.
type OrderSummary struct {
	ID            int     `json:"id"`
	SourceUUID    string  `json:"source_uuid,omitempty"`
	StatusID      int     `json:"status_id,omitempty"`
	Status        string  `json:"status,omitempty"`
	PaymentStatus string  `json:"payment_status,omitempty"`
	GrandTotal    float64 `json:"grand_total"`
	BuyerName     string  `json:"buyer_name,omitempty"`
	CreatedAt     string  `json:"created_at,omitempty"`
}

// rawOrder — поля замовлення keyCRM, з яких будується OrderSummary.
type rawOrder struct {
	ID            int       `json:"id"`
	SourceUUID    flexStr   `json:"source_uuid"`
	StatusID      int       `json:"status_id"`
	PaymentStatus string    `json:"payment_status"`
	GrandTotal    flexFloat `json:"grand_total"`
	CreatedAt     string    `json:"created_at"`
	Status        *struct {
		Name string `json:"name"`
	} `json:"status"`
	Buyer *struct {
		FullName string `json:"full_name"`
	} `json:"buyer"`
}

func summarize(o rawOrder) OrderSummary {
	s := OrderSummary{
		ID:            o.ID,
		SourceUUID:    string(o.SourceUUID),
		StatusID:      o.StatusID,
		PaymentStatus: o.PaymentStatus,
		GrandTotal:    float64(o.GrandTotal),
		CreatedAt:     o.CreatedAt,
	}
	if o.Status != nil {
		s.Status = o.Status.Name
	}
	if o.Buyer != nil {
		s.BuyerName = o.Buyer.FullName
	}
	return s
}

// flexFloat приймає число як JSON-число або рядок ("123.50"): суми в keyCRM
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
		return fmt.Errorf("amount %s: %w", b, err)
	}
	*f = flexFloat(v)
	return nil
}

// flexStr приймає рядок або число (номер замовлення в джерелі буває числом).
type flexStr string

func (s *flexStr) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = ""
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = flexStr(str)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("source_uuid %s: %w", b, err)
	}
	*s = flexStr(n.String())
	return nil
}
