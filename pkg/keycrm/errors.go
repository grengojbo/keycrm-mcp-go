// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Error — відповідь keyCRM зі статусом не 2xx.
type Error struct {
	// Status — HTTP-статус відповіді.
	Status int
	// Message — повідомлення keyCRM або текст статусу.
	Message string
	// Fields — помилки валідації по полях (422).
	Fields map[string][]string
}

// Error реалізує error.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "keycrm: %d %s", e.Status, e.Message)
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "; %s: %s", k, strings.Join(e.Fields[k], ", "))
		}
	}
	return b.String()
}

// CheckResponse повертає nil для 2xx, інакше *Error з повідомленням keyCRM
// і, для 422, помилками по полях.
func CheckResponse(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	e := &Error{Status: status}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		e.Message = "invalid or missing API key"
		return e
	}
	var payload struct {
		Message string              `json:"message"`
		Errors  map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		e.Message = payload.Message
		if len(payload.Errors) > 0 {
			e.Fields = payload.Errors
		}
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	return e
}
