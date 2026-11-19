// SPDX-License-Identifier: AGPL-3.0-only

// Package receipts — прибуткові накладні: середньозважена собівартість,
// надходження товару на склад і відкат надходження (ТЗ §7).
package receipts

import (
	"errors"
	"math"
)

var (
	// ErrStockToZero — після відкату загальний залишок не більший за нуль,
	// тож собівартість зворотною формулою не порахувати.
	ErrStockToZero = errors.New("stock goes to zero or below")
	// ErrNegativeCost — зворотна формула дала від'ємну собівартість: між
	// надходженням і відкатом були інші рухи або ціна відкату завелика.
	ErrNegativeCost = errors.New("reverse cost would be negative")
)

// Round2 округлює до копійок за правилом half-up (половина — від нуля).
// Спершу прибирається шум двійкового представлення: 2.675 у float64 — це
// 2.67499999…, і без поправки воно округлилося б до 2.67.
func Round2(v float64) float64 {
	cents := math.Round(v*100*1e6) / 1e6
	r := math.Round(cents) / 100
	if r == 0 {
		return 0 // без від'ємного нуля
	}
	return r
}

// WeightedCost повертає середньозважену собівартість після надходження inQty
// одиниць по inCost до залишку qty по cost. Якщо залишок не додатний, нова
// собівартість дорівнює ціні надходження. Результат округлено Round2.
func WeightedCost(qty, cost, inQty, inCost float64) float64 {
	if qty <= 0 {
		return Round2(inCost)
	}
	return Round2((qty*cost + inQty*inCost) / (qty + inQty))
}

// ReverseCost повертає собівартість після відкату надходження outQty одиниць
// по outCost із залишку qty по cost. Якщо залишок після відкату не додатний —
// ErrStockToZero; якщо результат від'ємний — ErrNegativeCost. Результат
// округлено Round2.
func ReverseCost(qty, cost, outQty, outCost float64) (float64, error) {
	rest := qty - outQty
	if rest <= 0 {
		return 0, ErrStockToZero
	}
	c := Round2((qty*cost - outQty*outCost) / rest)
	if c < 0 {
		return 0, ErrNegativeCost
	}
	return c, nil
}
