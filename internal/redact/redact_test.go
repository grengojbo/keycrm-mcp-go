// SPDX-License-Identifier: AGPL-3.0-only

package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPhone(t *testing.T) {
	cases := map[string]string{
		"+380671234567": "+38067*****67",
		"0671234567":    "067123**67",
		"12345":         "***",
		"":              "",
	}
	for in, want := range cases {
		if got := Phone(in); got != want {
			t.Errorf("Phone(%q) = %q, очікувано %q", in, got, want)
		}
	}
}

func TestEmail(t *testing.T) {
	cases := map[string]string{
		"oleg@example.com": "o***@example.com",
		"x@y.z":            "x***@y.z",
		"broken":           "***",
	}
	for in, want := range cases {
		if got := Email(in); got != want {
			t.Errorf("Email(%q) = %q, очікувано %q", in, got, want)
		}
	}
}

func TestRedactNested(t *testing.T) {
	in := map[string]any{
		"buyer": map[string]any{"full_name": "Іван", "phone": []any{"+380671234567"}},
		"sku":   "A1",
	}
	got := Value(in).(map[string]any)
	buyer := got["buyer"].(map[string]any)
	if buyer["full_name"] == "Іван" {
		t.Error("ім'я не замасковано")
	}
	if p := buyer["phone"].([]any)[0]; p != "+38067*****67" {
		t.Errorf("телефон = %v", p)
	}
	if got["sku"] != "A1" {
		t.Errorf("sku змінено: %v", got["sku"])
	}
	// Оригінал не змінюється.
	if in["buyer"].(map[string]any)["full_name"] != "Іван" {
		t.Error("Value змінив вхідне значення")
	}
}

func TestRedactStructAndContexts(t *testing.T) {
	type product struct {
		Name string `json:"name"`
		SKU  string `json:"sku"`
	}
	type shipping struct {
		Name    string `json:"name"`
		City    string `json:"shipping_address_city"`
		Phone   string `json:"recipient_phone"`
		Comment string `json:"note"`
	}
	type input struct {
		Products   []product `json:"products"`
		Shipping   shipping  `json:"shipping"`
		BuyerPhone string    `json:"buyer_phone"`
		BuyerEmail string    `json:"buyer_email"`
		Comment    string    `json:"manager_comment"`
		StatusID   int       `json:"status_id"`
		Supplier   string    `json:"supplier"`
	}
	v := Value(input{
		Products:   []product{{Name: "Філамент PLA", SKU: "PLA-1"}},
		Shipping:   shipping{Name: "Петро", City: "Київ", Phone: "+380501112233", Comment: "дзвонити ввечері"},
		BuyerPhone: "+380671234567",
		BuyerEmail: "ivan@example.com",
		Comment:    "VIP",
		StatusID:   3,
		Supplier:   "ФОП Коваленко",
	})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, leak := range []string{"Петро", "Київ", "+380501112233", "дзвонити", "+380671234567", "ivan@", "VIP", "Коваленко"} {
		if strings.Contains(s, leak) {
			t.Errorf("у результаті лишилось %q: %s", leak, s)
		}
	}
	for _, keep := range []string{"Філамент PLA", "PLA-1", `"status_id":3`} {
		if !strings.Contains(s, keep) {
			t.Errorf("у результаті немає %q: %s", keep, s)
		}
	}
}

func TestRedactUnmarshalable(t *testing.T) {
	if got := Value(make(chan int)); got != "[unloggable]" {
		t.Errorf("Value(chan) = %v", got)
	}
}
