// SPDX-License-Identifier: AGPL-3.0-only

package receipts

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// fakeOffer — стан варіанту у фейковому keyCRM.
type fakeOffer struct {
	ID         int
	SKU        string
	Cost       float64
	Warehouses map[int]float64
}

// stockPut і offerPut — тіла записів, які отримав фейк.
type stockPut struct {
	WarehouseID int `json:"warehouse_id"`
	Stocks      []struct {
		ID       int     `json:"id"`
		SKU      string  `json:"sku"`
		Quantity float64 `json:"quantity"`
	} `json:"stocks"`
}

type offerPut struct {
	Offers []map[string]any `json:"offers"`
}

// fakeKeyCRM емулює GET /offers/stocks, PUT /offers/stocks і PUT /offers
// зі збереженням стану складів і собівартостей.
type fakeKeyCRM struct {
	t *testing.T

	mu        sync.Mutex
	offers    []*fakeOffer
	gets      int
	stockPuts []stockPut
	offerPuts []offerPut

	// failOffers — PUT /offers повертає 500.
	failOffers bool
	// failStocks — PUT /offers/stocks повертає 422.
	failStocks bool
	// afterOfferPut викликається після застосування PUT /offers (під м'ютексом).
	afterOfferPut func(f *fakeKeyCRM)
	// afterStockPut викликається після застосування PUT /offers/stocks (під м'ютексом).
	afterStockPut func(f *fakeKeyCRM)
}

func newFake(t *testing.T, offers ...*fakeOffer) *fakeKeyCRM {
	return &fakeKeyCRM{t: t, offers: offers}
}

// client повертає клієнт keyCRM, спрямований на фейк.
func (f *fakeKeyCRM) client() *keycrm.Client {
	return server.FakeKeyCRM(f.t, f)
}

func (f *fakeKeyCRM) find(id int, sku string) *fakeOffer {
	for _, o := range f.offers {
		if (id > 0 && o.ID == id) || (sku != "" && o.SKU == sku) {
			return o
		}
	}
	return nil
}

func (f *fakeKeyCRM) writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stockPuts) + len(f.offerPuts)
}

func (f *fakeKeyCRM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/offers/stocks":
		f.gets++
		f.serveStocks(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/offers/stocks":
		var body stockPut
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("PUT /offers/stocks: %v", err)
		}
		f.stockPuts = append(f.stockPuts, body)
		if f.failStocks {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"warehouse_id":["invalid"]}}`)
			return
		}
		for _, s := range body.Stocks {
			o := f.find(s.ID, s.SKU)
			if o == nil {
				f.t.Errorf("PUT /offers/stocks: невідомий варіант %+v", s)
				continue
			}
			o.Warehouses[body.WarehouseID] = s.Quantity
		}
		if f.afterStockPut != nil {
			f.afterStockPut(f)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":true}`)
	case r.Method == http.MethodPut && r.URL.Path == "/offers":
		var body offerPut
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("PUT /offers: %v", err)
		}
		f.offerPuts = append(f.offerPuts, body)
		if f.failOffers {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"Server Error"}`)
			return
		}
		for _, u := range body.Offers {
			id, _ := u["id"].(float64)
			sku, _ := u["sku"].(string)
			o := f.find(int(id), sku)
			if o == nil {
				f.t.Errorf("PUT /offers: невідомий варіант %v", u)
				continue
			}
			if p, ok := u["purchased_price"].(float64); ok {
				o.Cost = p
			}
		}
		if f.afterOfferPut != nil {
			f.afterOfferPut(f)
		}
		_, _ = io.WriteString(w, `{"status":true}`)
	default:
		f.t.Errorf("неочікуваний запит %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeKeyCRM) serveStocks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("filter[details]") != "true" {
		f.t.Errorf("запит залишків без details: %s", r.URL.RawQuery)
	}
	var match func(o *fakeOffer) bool
	switch {
	case q.Get("filter[offers_sku]") != "":
		skus := strings.Split(q.Get("filter[offers_sku]"), ",")
		match = func(o *fakeOffer) bool { return slices.Contains(skus, o.SKU) }
	case q.Get("filter[offers_id]") != "":
		ids := strings.Split(q.Get("filter[offers_id]"), ",")
		match = func(o *fakeOffer) bool { return slices.Contains(ids, strconv.Itoa(o.ID)) }
	default:
		f.t.Errorf("запит залишків без фільтра: %s", r.URL.RawQuery)
		match = func(*fakeOffer) bool { return false }
	}
	type wh struct {
		ID       int     `json:"id"`
		Quantity float64 `json:"quantity"`
		Reserve  float64 `json:"reserve"`
	}
	type row struct {
		ID             int    `json:"id"`
		SKU            string `json:"sku"`
		PurchasedPrice string `json:"purchased_price"`
		Warehouse      []wh   `json:"warehouse"`
	}
	data := []row{}
	for _, o := range f.offers {
		if !match(o) {
			continue
		}
		rw := row{ID: o.ID, SKU: o.SKU, PurchasedPrice: fmt.Sprintf("%.2f", o.Cost), Warehouse: []wh{}}
		ids := make([]int, 0, len(o.Warehouses))
		for id := range o.Warehouses {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			rw.Warehouse = append(rw.Warehouse, wh{ID: id, Quantity: o.Warehouses[id]})
		}
		data = append(data, rw)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "next_page_url": nil})
}
