// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	"context"
	"fmt"
)

// ListAll збирає елементи посторінково (сторінки з 1), доки fetch повідомляє
// про наступну сторінку і не набрано maxItems елементів. Зайві елементи останньої
// сторінки відкидаються.
func ListAll[T any](ctx context.Context, maxItems int, fetch func(ctx context.Context, page int) (items []T, hasNext bool, err error)) ([]T, error) {
	var all []T
	for page := 1; len(all) < maxItems; page++ {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		items, hasNext, err := fetch(ctx, page)
		if err != nil {
			return all, fmt.Errorf("keycrm: page %d: %w", page, err)
		}
		all = append(all, items...)
		if !hasNext || len(items) == 0 {
			break
		}
	}
	if len(all) > maxItems {
		all = all[:maxItems]
	}
	return all, nil
}
