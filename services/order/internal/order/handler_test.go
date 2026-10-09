package order

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

func newTestRouter(peers *fakePeers) (*gin.Engine, *fakeStore) {
	store := newFakeStore()
	handler := NewHandler(NewCheckout(store, peers.asPeers(), discardLogger), store)
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "order-service-test",
		Logger:         discardLogger,
		Ready:          func(context.Context) error { return nil },
		RegisterRoutes: handler.RegisterRoutes,
	}), store
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

func TestCheckoutEndpointShipped(t *testing.T) {
	router, _ := newTestRouter(newFakePeers())

	rec := doRequest(router, http.MethodPost, "/orders/checkout", `{"user_id": 1, "product_id": "PROD_0001", "attribution_id": 99}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	got := decode[orderResponse](t, rec)
	assert.Equal(t, StatusShipped, got.Status)
	assert.Equal(t, 1, got.Quantity, "quantity defaults to 1")
	assert.Equal(t, "49.99", *got.TotalAmount)
	assert.Equal(t, "1ZTRACK", got.TrackingNumber)
	assert.Nil(t, got.FailureReason)
}

func TestCheckoutEndpointFailureStatuses(t *testing.T) {
	cases := map[string]struct {
		setup      func(p *fakePeers)
		wantStatus int
		wantCode   string
	}{
		"business failure is 422": {func(p *fakePeers) { p.reserveErr = ErrOutOfStock }, http.StatusUnprocessableEntity, "OUT_OF_STOCK"},
		"decline is 422":          {func(p *fakePeers) { p.chargeErr = ErrPaymentDeclined }, http.StatusUnprocessableEntity, "PAYMENT_DECLINED"},
		"dependency down is 502":  {func(p *fakePeers) { p.customerErr = errors.New("down") }, http.StatusBadGateway, "UPSTREAM_ERROR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			peers := newFakePeers()
			tc.setup(peers)
			router, _ := newTestRouter(peers)

			rec := doRequest(router, http.MethodPost, "/orders/checkout", `{"user_id": 1, "product_id": "PROD_0001"}`)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			got := decode[checkoutFailure](t, rec)
			assert.Equal(t, tc.wantCode, got.Error)
			assert.NotEmpty(t, got.Message)
			assert.NotZero(t, got.Order.OrderID, "the failed order's ID is returned")
			assert.Equal(t, StatusFailed, got.Order.Status)
		})
	}
}

func TestCheckoutEndpointPaidAwaitingShipmentIs201(t *testing.T) {
	peers := newFakePeers()
	peers.shipErr = errors.New("down")
	router, _ := newTestRouter(peers)

	rec := doRequest(router, http.MethodPost, "/orders/checkout", `{"user_id": 1, "product_id": "PROD_0001"}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, StatusPaid, decode[orderResponse](t, rec).Status)
}

func TestCheckoutEndpointRejectsInvalidInput(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":        `{`,
		"missing user":     `{"product_id": "PROD_0001"}`,
		"missing product":  `{"user_id": 1}`,
		"zero attribution": `{"user_id": 1, "product_id": "PROD_0001", "attribution_id": 0}`,
		"quantity too big": `{"user_id": 1, "product_id": "PROD_0001", "quantity": 101}`,
	} {
		t.Run(name, func(t *testing.T) {
			router, store := newTestRouter(newFakePeers())

			rec := doRequest(router, http.MethodPost, "/orders/checkout", body)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, store.orders, "invalid requests create no order")
		})
	}
}

func TestGetOrder(t *testing.T) {
	router, _ := newTestRouter(newFakePeers())
	doRequest(router, http.MethodPost, "/orders/checkout", `{"user_id": 1, "product_id": "PROD_0001"}`)

	rec := doRequest(router, http.MethodGet, "/orders/1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, StatusShipped, decode[orderResponse](t, rec).Status)

	rec = doRequest(router, http.MethodGet, "/orders/42", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, codeOrderNotFound, decode[httpserver.ErrorResponse](t, rec).Error)
}

func TestCheckoutMetrics(t *testing.T) {
	peers := newFakePeers()
	peers.reserveErr = ErrOutOfStock
	router, _ := newTestRouter(peers)
	doRequest(router, http.MethodPost, "/orders/checkout", `{"user_id": 1, "product_id": "PROD_0001"}`)

	metrics := doRequest(router, http.MethodGet, "/metrics", "").Body.String()
	assert.Contains(t, metrics, `orders_checkouts_total{reason="OUT_OF_STOCK",status="FAILED"}`)
}
