// SPDX-License-Identifier: AGPL-3.0-only

package buyers

import "testing"

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "067 123 45 67", want: "+380671234567"},
		{in: "+38 (067) 123-45-67", want: "+380671234567"},
		{in: "380671234567", want: "+380671234567"},
		{in: "0671234567", want: "+380671234567"},
		{in: "+380671234567", want: "+380671234567"},
		{in: "8 067 123 45 67", want: "+380671234567"},
		{in: " 067.123.45.67 ", want: "+380671234567"},
		{in: "+1 555-234-1234", want: "+15552341234"},
		{in: "+48 512 345 678", want: "+48512345678"},
		{in: "123", wantErr: true},
		{in: "", wantErr: true},
		{in: "067 123 45", wantErr: true},
		{in: "06712345678", wantErr: true},
		{in: "+380 67 123 456", wantErr: true},
		{in: "38067123456789", wantErr: true},
		{in: "+1234567890123456", wantErr: true},
		{in: "5552341234", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizePhone(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Errorf("очікувалась помилка, отримано %q", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("NormalizePhone(%q) = %q, %v; хочемо %q", tt.in, got, err, tt.want)
			}
		})
	}
}
