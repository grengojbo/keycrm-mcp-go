// SPDX-License-Identifier: AGPL-3.0-only

package receipts

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestRound2HalfUp(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0},
		{53, 53},
		{10.004, 10},
		{10.005, 10.01},
		{1.005, 1.01}, // у float64 це 1.00499999…
		{2.675, 2.68}, // у float64 це 2.67499999…
		{57.5, 57.5},
		{-1.005, -1.01},
		{-0.004, 0},
		{775.0000000001, 775},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.in), func(t *testing.T) {
			got := Round2(tc.in)
			if got != tc.want || math.Signbit(got) != math.Signbit(tc.want) {
				t.Errorf("Round2(%v) = %v, очікувалось %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestWeightedCost(t *testing.T) {
	cases := []struct {
		name                     string
		qty, cost, inQty, inCost float64
		want                     float64
	}{
		{"2 по 50 + 1 по 59", 2, 50, 1, 59, 53},
		{"нульовий залишок", 0, 50, 3, 40, 40},
		{"від'ємний залишок", -2, 50, 3, 40, 40},
		{"дробові кількості", 0.75, 800, 1.25, 760, 775},
		{"округлення вниз", 3, 10, 1, 10.005, 10},
		{"округлення half-up", 1, 10, 2, 10.015, 10.01},
		{"залишок по всіх складах", 6, 50, 2, 80, 57.5},
		{"нульова ціна надходження", 1, 30, 1, 0, 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WeightedCost(tc.qty, tc.cost, tc.inQty, tc.inCost); got != tc.want {
				t.Errorf("WeightedCost(%v, %v, %v, %v) = %v, очікувалось %v", tc.qty, tc.cost, tc.inQty, tc.inCost, got, tc.want)
			}
		})
	}
}

func TestReverseCost(t *testing.T) {
	cases := []struct {
		name                       string
		qty, cost, outQty, outCost float64
		want                       float64
		wantErr                    error
	}{
		{"3 по 53 мінус 1 по 59", 3, 53, 1, 59, 50, nil},
		{"дробові кількості", 2, 775, 1.25, 760, 800, nil},
		{"округлення half-up", 3, 10.01, 2, 10.015, 10, nil},
		{"залишок стає нулем", 1, 59, 1, 59, 0, ErrStockToZero},
		{"залишок стає від'ємним", 1, 59, 2, 59, 0, ErrStockToZero},
		{"від'ємна собівартість", 2, 50, 1, 200, 0, ErrNegativeCost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReverseCost(tc.qty, tc.cost, tc.outQty, tc.outCost)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("помилка %v, очікувалось %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("ReverseCost(%v, %v, %v, %v) = %v, очікувалось %v", tc.qty, tc.cost, tc.outQty, tc.outCost, got, tc.want)
			}
		})
	}
}

// TestReverseUndoesWeighted перевіряє, що відкат повертає собівартість до
// надходження, якщо між ними не було інших рухів.
func TestReverseUndoesWeighted(t *testing.T) {
	cases := []struct{ qty, cost, q, c float64 }{
		{2, 50, 1, 59},
		{0.75, 800, 1.25, 760},
		{10, 12.5, 4, 14},
	}
	for _, tc := range cases {
		after := WeightedCost(tc.qty, tc.cost, tc.q, tc.c)
		back, err := ReverseCost(tc.qty+tc.q, after, tc.q, tc.c)
		if err != nil || back != tc.cost {
			t.Errorf("%+v: після надходження %v, після відкату %v (%v)", tc, after, back, err)
		}
	}
}
