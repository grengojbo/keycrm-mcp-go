// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"context"
	"errors"
	"testing"
)

// pages повертає fetch, що віддає по per елементів на сторінку з total усього.
func pages(total, per int, calls *int) func(context.Context, int) ([]int, bool, error) {
	return func(_ context.Context, page int) ([]int, bool, error) {
		*calls++
		start := (page - 1) * per
		var items []int
		for i := start; i < start+per && i < total; i++ {
			items = append(items, i)
		}
		return items, start+per < total, nil
	}
}

func TestListAllStopsAtMax(t *testing.T) {
	var calls int
	got, err := ListAll(context.Background(), 7, pages(100, 3, &calls))
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 7 || got[6] != 6 {
		t.Errorf("отримано %v, очікується 7 елементів 0..6", got)
	}
	if calls != 3 {
		t.Errorf("сторінок запитано %d, очікується 3", calls)
	}
}

func TestListAllStopsWhenNoNext(t *testing.T) {
	var calls int
	got, err := ListAll(context.Background(), 1000, pages(8, 3, &calls))
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 8 {
		t.Errorf("отримано %d, очікується 8", len(got))
	}
	if calls != 3 {
		t.Errorf("сторінок запитано %d, очікується 3", calls)
	}
}

func TestListAllPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	_, err := ListAll(context.Background(), 10, func(context.Context, int) ([]int, bool, error) {
		return nil, false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("помилка = %v, очікується обгорнута boom", err)
	}
}

func TestListAllHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	_, err := ListAll(ctx, 10, pages(100, 3, &calls))
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("помилка = %v, сторінок %d; очікується context.Canceled і 0", err, calls)
	}
}
