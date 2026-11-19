// SPDX-License-Identifier: AGPL-3.0-only

package payments

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

func connect(t *testing.T, h http.HandlerFunc, readOnly bool) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, readOnly)
}

func TestListExternalTransactionsFiltersAndPaging(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/payments/external-transactions" || q.Get("limit") != "50" || q.Get("page") != "2" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if q.Get("filter[is_attached]") != "false" || q.Get("filter[gateway_id]") != "1,2" ||
			q.Get("filter[created_between]") != "2026-11-01 00:00:00,2026-11-30 23:59:59" {
			t.Errorf("фільтри: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":60,"current_page":2,"data":[{"id":22,"amount":122.5,"currency":"UAH"}],"next_page_url":"x?page=3"}`)
	}, false)
	res := server.CallTool(t, cs, "list_external_transactions", map[string]any{
		"is_attached": false, "gateway_id": []int{1, 2}, "limit": 80, "page": 2,
		"created_between": "2026-11-01 00:00:00,2026-11-30 23:59:59",
	})
	out := server.Decode[listing.Result[map[string]any]](t, res)
	if out.Note != "limit capped at 50" || !out.HasNext || out.Total != 60 || len(out.Items) != 1 || out.Items[0]["id"] != float64(22) {
		t.Errorf("результат: %+v", out)
	}
}

func TestAttachExternalTransaction(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/payments/77/external-transactions" {
			t.Errorf("запит %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["transaction_id"] != float64(22) || len(body) != 1 {
			t.Errorf("тіло: %v (%v)", body, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":77,"status":"paid"}`)
	}, false)
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "attach_external_transaction", map[string]any{"payment_id": 77, "transaction_id": 22}))
	if out["id"] != float64(77) {
		t.Errorf("результат: %v", out)
	}
}

func TestAttachExternalTransactionNeedsTransaction(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, false)
	res := server.CallTool(t, cs, "attach_external_transaction", map[string]any{"payment_id": 77})
	if !res.IsError || calls.Load() != 0 {
		t.Errorf("IsError=%v, запитів=%d", res.IsError, calls.Load())
	}
}

func TestAttachExternalTransaction422IsError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"transaction_id":["Транзакцію вже прив'язано"]}}`)
	}, false)
	res := server.CallTool(t, cs, "attach_external_transaction", map[string]any{"payment_id": 77, "transaction_id": 22})
	if !res.IsError || server.ResultText(res) == "" {
		t.Errorf("очікувалась помилка: %s", server.ResultText(res))
	}
}

func TestReadOnlyPayments(t *testing.T) {
	cs := connect(t, func(http.ResponseWriter, *http.Request) {}, true)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "list_external_transactions" {
		t.Errorf("read-only інструменти: %v", res.Tools)
	}
}
