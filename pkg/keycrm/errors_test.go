// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestCheckResponse422Fields(t *testing.T) {
	err := CheckResponse(http.StatusUnprocessableEntity, []byte(`{"message":"The given data was invalid.","errors":{"sku":["required"]}}`))
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("помилка = %v, очікується *Error", err)
	}
	if ke.Status != http.StatusUnprocessableEntity || ke.Message != "The given data was invalid." {
		t.Errorf("помилка = %+v", ke)
	}
	if len(ke.Fields["sku"]) == 0 || ke.Fields["sku"][0] != "required" {
		t.Errorf("Fields = %v", ke.Fields)
	}
	if !strings.Contains(ke.Error(), "sku: required") {
		t.Errorf("Error() = %q, очікується згадка поля", ke.Error())
	}
}

func TestCheckResponse(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantNil bool
		wantMsg string
	}{
		{"200", http.StatusOK, `{}`, true, ""},
		{"201", http.StatusCreated, ``, true, ""},
		{"401", http.StatusUnauthorized, `{"message":"Unauthenticated."}`, false, "invalid or missing API key"},
		{"403", http.StatusForbidden, ``, false, "invalid or missing API key"},
		{"404 з повідомленням", http.StatusNotFound, `{"message":"Not found"}`, false, "Not found"},
		{"500 без JSON", http.StatusInternalServerError, `<html>oops</html>`, false, "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckResponse(tt.status, []byte(tt.body))
			if tt.wantNil {
				if err != nil {
					t.Fatalf("помилка = %v, очікується nil", err)
				}
				return
			}
			var ke *Error
			if !errors.As(err, &ke) {
				t.Fatalf("помилка = %v, очікується *Error", err)
			}
			if ke.Status != tt.status || ke.Message != tt.wantMsg {
				t.Errorf("помилка = %+v, очікується статус %d і повідомлення %q", ke, tt.status, tt.wantMsg)
			}
		})
	}
}
