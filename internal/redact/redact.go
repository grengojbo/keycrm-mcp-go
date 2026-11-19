// SPDX-License-Identifier: AGPL-3.0-only

// Package redact маскує персональні дані перед записом у логи.
package redact

import (
	"encoding/json"
	"strings"
)

// mask — заміна для значень, яких не видно навіть частково.
const mask = "***"

// personContexts — об'єкти, у яких name/full_name належать людині, а не товару.
var personContexts = map[string]bool{"buyer": true, "contact": true, "shipping": true, "client": true}

// sensitiveKeys маскуються за будь-якої вкладеності.
var sensitiveKeys = map[string]bool{
	"full_name":           true,
	"recipient_full_name": true,
	"buyer_name":          true,
	"birthday":            true,
	"note":                true,
	"comment":             true,
	"buyer_comment":       true,
	"manager_comment":     true,
	"gift_message":        true,
	"supplier":            true, // постачальник прибуткової накладної: часто ФОП, тобто ім'я людини
}

// Value повертає копію v, придатну для логів: телефони й email частково
// замасковано, імена, адреси, дати народження й нотатки — повністю. Поле name
// маскується лише всередині об'єктів buyer, contact, shipping, client (поза
// ними це, наприклад, назва товару). Значення, яке не серіалізується в JSON,
// замінюється на "[unloggable]".
func Value(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return "[unloggable]"
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return "[unloggable]"
	}
	return walk(generic, "")
}

func walk(v any, parent string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = field(k, val, parent)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = walk(val, parent)
		}
		return t
	default:
		return v
	}
}

// field маскує значення ключа key, що лежить в об'єкті з назвою parent.
func field(key string, v any, parent string) any {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "phone"):
		return apply(v, Phone)
	case strings.Contains(k, "email"):
		return apply(v, Email)
	case sensitiveKeys[k], strings.Contains(k, "address"),
		k == "name" && personContexts[parent]:
		return apply(v, func(string) string { return mask })
	}
	return walk(v, k)
}

// apply застосовує f до рядка або до кожного рядка в масиві; інші значення
// (числа, об'єкти) повністю маскуються.
func apply(v any, f func(string) string) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return f(t)
	case []any:
		for i, el := range t {
			t[i] = apply(el, f)
		}
		return t
	default:
		return mask
	}
}

// Phone маскує середину номера: "+380671234567" → "+38067*****67".
// Короткі значення маскуються повністю.
func Phone(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	if len(r) <= 8 {
		return mask
	}
	return string(r[:6]) + strings.Repeat("*", len(r)-8) + string(r[len(r)-2:])
}

// Email лишає першу літеру й домен: "oleg@example.com" → "o***@example.com".
func Email(s string) string {
	at := strings.LastIndex(s, "@")
	if at <= 0 {
		if s == "" {
			return ""
		}
		return mask
	}
	first := []rune(s[:at])[0]
	return string(first) + mask + s[at:]
}
