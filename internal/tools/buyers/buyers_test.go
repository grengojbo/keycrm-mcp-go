// SPDX-License-Identifier: AGPL-3.0-only

package buyers

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

// connect підключає клієнта MCP до групи buyers поверх фейкового keyCRM.
func connect(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, false)
}

const oneBuyer = `{"total":1,"current_page":1,"next_page_url":null,"data":[
	{"id":4545,"full_name":"Іван Петренко","phone":["+380671234567"],"email":["ivan@example.com"],"note":"VIP","created_at":"2026-11-18 10:00:00"}]}`

func TestFindBuyerByPhoneUsesFilter(t *testing.T) {
	for _, phone := range []string{"067 123 45 67", "+38 (067) 123-45-67", "380671234567"} {
		t.Run(phone, func(t *testing.T) {
			cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/buyer" || q.Get("filter[buyer_phone]") != "+380671234567" || q.Has("filter[buyer_email]") {
					t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
				}
				_, _ = io.WriteString(w, oneBuyer)
			})
			out := server.Decode[FindBuyerResult](t, server.CallTool(t, cs, "find_buyer", map[string]any{"phone": phone}))
			want := BuyerSummary{ID: 4545, FullName: "Іван Петренко", Phone: []string{"+380671234567"}, Email: []string{"ivan@example.com"}, CreatedAt: "2026-11-18 10:00:00"}
			if out.Phone != "+380671234567" || len(out.Items) != 1 || !reflect.DeepEqual(out.Items[0], want) {
				t.Errorf("результат: %+v", out)
			}
			if !strings.Contains(out.Hint, "list_orders") {
				t.Errorf("підказка: %q", out.Hint)
			}
		})
	}
}

func TestFindBuyerByEmail(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("filter[buyer_email]") != "ivan@example.com" || q.Has("filter[buyer_phone]") {
			t.Errorf("фільтри: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, oneBuyer)
	})
	out := server.Decode[FindBuyerResult](t, server.CallTool(t, cs, "find_buyer", map[string]any{"email": " Ivan@Example.com "}))
	if out.Email != "ivan@example.com" || len(out.Items) != 1 || !strings.Contains(out.Hint, "buyer_email") {
		t.Errorf("результат: %+v", out)
	}
}

func TestFindBuyerNotFound(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"total":0,"current_page":1,"next_page_url":null,"data":[]}`)
	})
	res := server.CallTool(t, cs, "find_buyer", map[string]any{"phone": "0671234567"})
	out := server.Decode[FindBuyerResult](t, res)
	if res.IsError || out.Items == nil || len(out.Items) != 0 || !strings.Contains(out.Hint, "not found") {
		t.Errorf("результат: %+v", out)
	}
}

func TestFindBuyerNeedsPhoneOrEmail(t *testing.T) {
	tests := map[string]map[string]any{
		"порожньо":         {},
		"пробіли":          {"phone": "  ", "email": " "},
		"короткий телефон": {"phone": "123"},
		"не email":         {"email": "ivan"},
		"два email":        {"email": "a@example.com,b@example.com"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			res := server.CallTool(t, cs, "find_buyer", args)
			if !res.IsError || calls.Load() != 0 {
				t.Errorf("IsError=%v, запитів=%d: %s", res.IsError, calls.Load(), server.ResultText(res))
			}
		})
	}
}

func TestListBuyersFiltersAndPaging(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("filter[buyer_id]") != "4545,4546" || q.Get("limit") != "50" || q.Get("page") != "2" ||
			q.Get("filter[created_between]") != "2026-11-01 00:00:00,2026-11-30 23:59:59" {
			t.Errorf("неочікуваний запит %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":60,"current_page":2,"next_page_url":"https://openapi.keycrm.app/v1/buyer?page=3","data":[{"id":4545,"full_name":"Іван","phone":null}]}`)
	})
	out := server.Decode[listing.Result[BuyerSummary]](t, server.CallTool(t, cs, "list_buyers", map[string]any{
		"buyer_id": []int{4545, 4546}, "limit": 100, "page": 2,
		"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59",
	}))
	if out.Note != "limit capped at 50" || !out.HasNext || out.Total != 60 || len(out.Items) != 1 || out.Items[0].FullName != "Іван" {
		t.Errorf("сторінка: %+v", out)
	}
}

func TestGetBuyerIncludesCustomFields(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/buyer/4545" {
			t.Errorf("шлях %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("include"); got != "manager,shipping,company,loyalty,custom_fields" {
			t.Errorf("include = %q", got)
		}
		_, _ = io.WriteString(w, `{"id":4545,"full_name":"Іван","custom_fields":[{"uuid":"CT_1001","value":"Опт"}]}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "get_buyer", map[string]any{"buyer_id": 4545}))
	if fields, _ := out["custom_fields"].([]any); len(fields) != 1 {
		t.Errorf("custom_fields: %v", out)
	}
}

func TestCreateBuyerSendsArrays(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/buyer" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"full_name": "Іван Петренко",
			"phone":     []any{"+380671234567", "+15552341234"},
			"email":     []any{"ivan@example.com"},
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":4547,"full_name":"Іван Петренко"}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_buyer", map[string]any{
		"full_name": " Іван Петренко ", "phone": []string{"067 123 45 67", "+1 555-234-1234"}, "email": []string{"ivan@example.com"},
	}))
	if out["id"] != float64(4547) {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateBuyerValidates(t *testing.T) {
	tests := map[string]map[string]any{
		"без імені":       {"full_name": " "},
		"поганий телефон": {"full_name": "Іван", "phone": []string{"123"}},
		"телефон рядком":  {"full_name": "Іван", "phone": "0671234567"},
		"невідоме поле":   {"full_name": "Іван", "confirm": true},
		"невідома асоц.":  {"full_name": "Іван", "shipping": []any{map[string]any{"street": "x"}}},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			res := server.CallTool(t, cs, "create_buyer", args)
			if !res.IsError || calls.Load() != 0 {
				t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
			}
		})
	}
}

func TestCreateBuyerShowsValidationError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"email.0":["The email.0 must be a valid email address."]}}`)
	})
	res := server.CallTool(t, cs, "create_buyer", map[string]any{"full_name": "Іван", "email": []string{"bad"}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "email.0") {
		t.Errorf("очікувалась помилка валідації: %s", server.ResultText(res))
	}
}

func TestUpdateBuyerSendsOnlyChanges(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/buyer/4545" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"note": "", "phone": []any{"+380671234567"}}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("тіло: %v", body)
		}
		_, _ = io.WriteString(w, `{"id":4545}`)
	})
	res := server.CallTool(t, cs, "update_buyer", map[string]any{"buyer_id": 4545, "note": "", "phone": []string{"0671234567"}})
	if res.IsError {
		t.Error(server.ResultText(res))
	}
}

func TestUpdateBuyerNeedsChanges(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "update_buyer", map[string]any{"buyer_id": 4545})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
	}
}

func TestImportBuyersNeedsConfirm(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "import_buyers", map[string]any{"buyers": []any{map[string]any{"full_name": "Іван"}}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d: %s", res.IsError, calls.Load(), server.ResultText(res))
	}
}

func TestImportBuyersValidatesEach(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	res := server.CallTool(t, cs, "import_buyers", map[string]any{"confirm": true, "buyers": []any{
		map[string]any{"full_name": "Іван", "phone": []string{"0671234567"}},
		map[string]any{"full_name": "Петро", "phone": []string{"12"}},
	}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "buyers[1].phone[0]") || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d: %s", res.IsError, calls.Load(), server.ResultText(res))
	}
}

func TestImportBuyersSendsBody(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/buyer/import" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Buyers []map[string]any `json:"buyers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Buyers) != 2 || !reflect.DeepEqual(body.Buyers[0]["phone"], []any{"+380671234567"}) {
			t.Errorf("тіло: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"status":true}`)
	})
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "import_buyers", map[string]any{"confirm": true, "buyers": []any{
		map[string]any{"full_name": "Іван", "phone": []string{"0671234567"}},
		map[string]any{"full_name": "Петро"},
	}}))
	if out["status"] != true || out["submitted"] != float64(2) {
		t.Errorf("результат: %v", out)
	}
}
