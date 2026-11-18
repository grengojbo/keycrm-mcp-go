// SPDX-License-Identifier: AGPL-3.0-only

package listing

import (
	"errors"
	"testing"

	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in          Page
		page, limit int
		note        string
	}{
		{Page{}, 1, 25, ""},
		{Page{Number: 3, Limit: 10}, 3, 10, ""},
		{Page{Number: -1, Limit: 50}, 1, 50, ""},
		{Page{Limit: 100}, 1, 50, "limit capped at 50"},
	}
	for _, c := range cases {
		page, limit, note := c.in.Normalize()
		if page != c.page || limit != c.limit || note != c.note {
			t.Errorf("%+v → %d %d %q", c.in, page, limit, note)
		}
	}
}

func TestDecode(t *testing.T) {
	body := `{"total":3,"current_page":2,"per_page":1,"data":[{"id":7}],"next_page_url":"https://x/?page=3"}`
	r, err := Decode[map[string]any](200, []byte(body), 2, "n")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0]["id"] != float64(7) || !r.HasNext || r.Total != 3 || r.Page != 2 || r.Note != "n" {
		t.Errorf("Decode = %+v", r)
	}
	r, err = Decode[map[string]any](200, []byte(`{"data":[],"next_page_url":null}`), 1, "")
	if err != nil || r.HasNext || r.Items == nil {
		t.Errorf("остання сторінка: %+v %v", r, err)
	}
	_, err = Decode[map[string]any](422, []byte(`{"message":"bad","errors":{"limit":["max 50"]}}`), 1, "")
	var kerr *keycrm.Error
	if !errors.As(err, &kerr) || kerr.Fields["limit"][0] != "max 50" {
		t.Errorf("422: %v", err)
	}
}

func TestDecodeObject(t *testing.T) {
	obj, err := DecodeObject(204, nil)
	if err != nil || obj["ok"] != true {
		t.Errorf("204: %v %v", obj, err)
	}
	obj, err = DecodeObject(200, []byte(`[1,2]`))
	if err != nil || obj["data"] == nil {
		t.Errorf("масив: %v %v", obj, err)
	}
	if _, err := DecodeObject(404, []byte(`{"message":"Not found"}`)); err == nil {
		t.Error("404 без помилки")
	}
}

func TestFilter(t *testing.T) {
	yes := false
	f := Filter{}
	f.Set("status_id", 3)
	f.Set("source_id", 0)
	f.Set("buyer_email", "  ")
	f.Set("payment_status", []string{"paid", " overpaid"})
	f.Set("id", []int{1, 2})
	f.Set("has_tracking_code", &yes)
	f.Set("flag", false)
	want := map[string]string{"status_id": "3", "payment_status": "paid,overpaid", "id": "1,2", "has_tracking_code": "false"}
	if len(f) != len(want) {
		t.Fatalf("Filter = %v", f)
	}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %v, очікувано %s", k, f[k], v)
		}
	}
	if (Filter{}).Param() != nil {
		t.Error("порожній фільтр має давати nil")
	}
}
