package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/chaos"
	"github.com/poph2/datastack/pkgs/httpserver"
)

// fakeStore is an in-memory Store with the same contract as Repository.
type fakeStore struct {
	products map[string]Product
}

func newFakeStore(products ...Product) *fakeStore {
	s := &fakeStore{products: map[string]Product{}}
	for _, p := range products {
		s.products[p.ProductID] = p
	}
	return s
}

func (s *fakeStore) List(context.Context) ([]Product, error) {
	products := make([]Product, 0, len(s.products))
	for _, p := range s.products {
		products = append(products, p)
	}
	sort.Slice(products, func(i, j int) bool { return products[i].ProductID < products[j].ProductID })
	return products, nil
}

func (s *fakeStore) Get(_ context.Context, productID string) (Product, error) {
	p, ok := s.products[productID]
	if !ok {
		return Product{}, ErrNotFound
	}
	return p, nil
}

func (s *fakeStore) Reserve(_ context.Context, productID string, quantity int) (Product, error) {
	p, ok := s.products[productID]
	if !ok {
		return Product{}, ErrNotFound
	}
	if p.StockLevel < quantity {
		return Product{}, ErrOutOfStock
	}
	p.StockLevel -= quantity
	s.products[productID] = p
	return p, nil
}

func (s *fakeStore) AddStock(_ context.Context, productID string, quantity int) (Product, error) {
	p, ok := s.products[productID]
	if !ok {
		return Product{}, ErrNotFound
	}
	p.StockLevel += quantity
	s.products[productID] = p
	return p, nil
}

func product(id, category string, stock int, price string) Product {
	return Product{ProductID: id, Category: category, StockLevel: stock, Price: decimal.RequireFromString(price), AutoRestock: true}
}

func newTestRouter(store Store) *gin.Engine {
	handler := NewHandler(store)
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "inventory-service-test",
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ready:          func(context.Context) error { return nil },
		RegisterRoutes: handler.RegisterRoutes,
	})
}

func doRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

func TestReserve(t *testing.T) {
	store := newFakeStore(product("PROD_A", "APPAREL", 5, "49.99"))
	router := newTestRouter(store)

	rec := doRequest(router, http.MethodPost, "/inventory/reserve", `{"product_id": "PROD_A", "quantity": 2}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{
		"product_id": "PROD_A",
		"quantity": 2,
		"unit_price": "49.99",
		"category": "APPAREL",
		"remaining_stock": 3
	}`, rec.Body.String())
	assert.Equal(t, 3, store.products["PROD_A"].StockLevel)
}

func TestReserveFailures(t *testing.T) {
	cases := map[string]struct {
		body       string
		wantStatus int
		wantCode   string
	}{
		"out of stock":     {`{"product_id": "PROD_A", "quantity": 6}`, http.StatusConflict, codeOutOfStock},
		"unknown product":  {`{"product_id": "NOPE", "quantity": 1}`, http.StatusNotFound, codeProductNotFound},
		"zero quantity":    {`{"product_id": "PROD_A", "quantity": 0}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"missing product":  {`{"quantity": 1}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"quantity too big": {`{"product_id": "PROD_A", "quantity": 10001}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore(product("PROD_A", "APPAREL", 5, "49.99"))
			router := newTestRouter(store)

			rec := doRequest(router, http.MethodPost, "/inventory/reserve", tc.body)

			require.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantCode, decode[httpserver.ErrorResponse](t, rec).Error)
			assert.Equal(t, 5, store.products["PROD_A"].StockLevel, "a failed reserve must not change stock")
		})
	}
}

func TestReleaseAndRestockAddStock(t *testing.T) {
	for _, path := range []string{"/inventory/release", "/inventory/restock"} {
		t.Run(path, func(t *testing.T) {
			store := newFakeStore(product("PROD_A", "APPAREL", 5, "49.99"))
			router := newTestRouter(store)

			rec := doRequest(router, http.MethodPost, path, `{"product_id": "PROD_A", "quantity": 4}`)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"product_id": "PROD_A", "quantity": 4, "remaining_stock": 9}`, rec.Body.String())

			rec = doRequest(router, http.MethodPost, path, `{"product_id": "NOPE", "quantity": 1}`)
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestGetProduct(t *testing.T) {
	router := newTestRouter(newFakeStore(product("PROD_A", "HOME", 7, "50")))

	rec := doRequest(router, http.MethodGet, "/inventory/PROD_A", "")

	require.Equal(t, http.StatusOK, rec.Code)
	got := decode[productResponse](t, rec)
	assert.Equal(t, "50.00", got.Price, "money always renders with two decimals")
	assert.Equal(t, 7, got.StockLevel)

	assert.Equal(t, http.StatusNotFound, doRequest(router, http.MethodGet, "/inventory/NOPE", "").Code)
}

func TestListProducts(t *testing.T) {
	router := newTestRouter(newFakeStore(
		product("PROD_B", "HOME", 1, "2.50"),
		product("PROD_A", "HOME", 1, "1.00"),
	))

	rec := doRequest(router, http.MethodGet, "/inventory", "")

	require.Equal(t, http.StatusOK, rec.Code)
	got := decode[[]productResponse](t, rec)
	require.Len(t, got, 2)
	assert.Equal(t, "PROD_A", got[0].ProductID)
	assert.Equal(t, "2.50", got[1].Price)
}

func TestReservationMetrics(t *testing.T) {
	router := newTestRouter(newFakeStore(product("PROD_A", "APPAREL", 1, "49.99")))

	doRequest(router, http.MethodPost, "/inventory/reserve", `{"product_id": "PROD_A", "quantity": 1}`)
	doRequest(router, http.MethodPost, "/inventory/reserve", `{"product_id": "PROD_A", "quantity": 1}`)

	metrics := doRequest(router, http.MethodGet, "/metrics", "").Body.String()
	assert.Contains(t, metrics, `inventory_reservations_total{outcome="reserved"}`)
	assert.Contains(t, metrics, `inventory_reservations_total{outcome="out_of_stock"}`)
}

// fakeDrainer drains the first n products with stock, in ID order.
type fakeDrainer struct {
	store *fakeStore
	err   error
}

func (d fakeDrainer) DrainStock(ctx context.Context, products int) ([]string, error) {
	if d.err != nil {
		return nil, d.err
	}
	all, _ := d.store.List(ctx)
	var drained []string
	for _, p := range all {
		if len(drained) == products {
			break
		}
		if p.StockLevel > 0 {
			p.StockLevel = 0
			d.store.products[p.ProductID] = p
			drained = append(drained, p.ProductID)
		}
	}
	return drained, nil
}

func newChaosRouter(t *testing.T, drainer Drainer) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	knobs, err := chaos.NewStore("inventory-service-test", logger)
	require.NoError(t, err)
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "inventory-service-test",
		Logger:         logger,
		Ready:          func(context.Context) error { return nil },
		RegisterRoutes: knobs.Routes(NewHandler(newFakeStore()).RegisterRoutes, NewChaosActions(drainer, logger).RegisterRoutes),
	})
}

func TestDrainStock(t *testing.T) {
	store := newFakeStore(
		product("PROD_A", "APPAREL", 5, "1.99"),
		product("PROD_B", "APPAREL", 0, "1.99"),
		product("PROD_C", "APPAREL", 9, "1.99"),
		product("PROD_D", "APPAREL", 3, "1.99"),
	)
	router := newChaosRouter(t, fakeDrainer{store: store})

	rec := doRequest(router, http.MethodPost, "/admin/chaos/drain-stock", `{"products": 2}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"drained": 2, "product_ids": ["PROD_A", "PROD_C"]}`, rec.Body.String())
	assert.Zero(t, store.products["PROD_C"].StockLevel)
	assert.Equal(t, 3, store.products["PROD_D"].StockLevel)
}

func TestDrainStockRejectsInvalidInput(t *testing.T) {
	router := newChaosRouter(t, fakeDrainer{store: newFakeStore()})

	for _, body := range []string{`{}`, `{"products": 0}`, `{"products": 101}`, `{"products": "x"}`, `{`} {
		rec := doRequest(router, http.MethodPost, "/admin/chaos/drain-stock", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.Equal(t, httpserver.CodeInvalidRequest, decode[httpserver.ErrorResponse](t, rec).Error)
	}
}

func TestDrainStockFailureIs500(t *testing.T) {
	router := newChaosRouter(t, fakeDrainer{err: errors.New("database down")})

	rec := doRequest(router, http.MethodPost, "/admin/chaos/drain-stock", `{"products": 1}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "database down")
}
