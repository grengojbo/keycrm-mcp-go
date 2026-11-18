// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import "testing"

func TestSpecHas66Operations(t *testing.T) {
	ids, err := SpecOperationIDs()
	if err != nil {
		t.Fatalf("SpecOperationIDs: %v", err)
	}
	if len(ids) != 66 {
		t.Errorf("кількість операцій = %d, очікується 66", len(ids))
	}
	for _, want := range []string{"offersStocksUpdate", "getPaginatedListOfOrders"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("немає операції %q", want)
		}
	}
}
