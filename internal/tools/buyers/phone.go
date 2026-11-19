// SPDX-License-Identifier: AGPL-3.0-only

package buyers

import (
	"errors"
	"fmt"
	"strings"
)

// Межі довжини номера в цифрах: менше 10 не буває навіть з кодом країни,
// більше 15 не дозволяє E.164.
const (
	minPhoneDigits = 10
	maxPhoneDigits = 15
)

// NormalizePhone зводить телефон до міжнародного формату +<цифри>, у якому
// keyCRM шукає покупців. Прибирає все, крім цифр і початкового «+».
// Українські номери: 0XXXXXXXXX → +380XXXXXXXXX, 380… → +380…, старий
// формат 80XXXXXXXXX → +380XXXXXXXXX. Інші міжнародні номери приймаються
// лише з «+», бо без нього код країни не вгадати.
func NormalizePhone(s string) (string, error) {
	s = strings.TrimSpace(s)
	plus := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) < minPhoneDigits {
		return "", fmt.Errorf("phone %q: too few digits, use the international format, e.g. +380671234567", s)
	}
	if len(d) > maxPhoneDigits {
		return "", fmt.Errorf("phone %q: too many digits", s)
	}
	switch {
	case plus:
	case strings.HasPrefix(d, "0"):
		d = "38" + d
	case strings.HasPrefix(d, "80") && len(d) == 11:
		d = "3" + d
	case strings.HasPrefix(d, "380"):
	default:
		return "", fmt.Errorf("phone %q: add the country code with +, e.g. +380671234567", s)
	}
	if strings.HasPrefix(d, "380") && len(d) != 12 {
		return "", errors.New("phone: a Ukrainian number has 9 digits after +380, e.g. +380671234567")
	}
	return "+" + d, nil
}
