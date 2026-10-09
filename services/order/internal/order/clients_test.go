package order

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpclient"
)

// peerServer answers one request with a fixed status and body, recording the
// request it received.
func peerServer(t *testing.T, status int, body string) (*httpclient.Client, *map[string]any) {
	t.Helper()
	received := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		received["_path"] = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return httpclient.New("peer", server.URL, time.Second), &received
}

// The response bodies below are copied from the peers' real handlers, so these
// tests pin the contract between the Order Service and each peer.

func TestCustomerClient(t *testing.T) {
	client, received := peerServer(t, http.StatusOK,
		`{"id":7,"email":"a@b.c","country":"GB","loyalty_tier":"GOLD","status":"ACTIVE"}`)

	customer, err := NewCustomerClient(client).Get(context.Background(), 7)

	require.NoError(t, err)
	assert.Equal(t, Customer{Country: "GB", LoyaltyTier: "GOLD", Status: "ACTIVE"}, customer)
	assert.Equal(t, "GET /customers/7", (*received)["_path"])

	client, _ = peerServer(t, http.StatusNotFound, `{"error":"CUSTOMER_NOT_FOUND","message":"customer not found"}`)
	_, err = NewCustomerClient(client).Get(context.Background(), 7)
	assert.ErrorIs(t, err, ErrCustomerNotFound)
}

func TestInventoryClient(t *testing.T) {
	client, received := peerServer(t, http.StatusOK,
		`{"product_id":"PROD_0001","quantity":2,"unit_price":"251.99","category":"ELECTRONICS","remaining_stock":75}`)

	price, err := NewInventoryClient(client).Reserve(context.Background(), "PROD_0001", 2)

	require.NoError(t, err)
	assert.Equal(t, "251.99", price.StringFixed(2))
	assert.Equal(t, "POST /inventory/reserve", (*received)["_path"])
	assert.Equal(t, "PROD_0001", (*received)["product_id"])
	assert.EqualValues(t, 2, (*received)["quantity"])

	for code, want := range map[string]error{"OUT_OF_STOCK": ErrOutOfStock, "PRODUCT_NOT_FOUND": ErrProductNotFound} {
		client, _ := peerServer(t, http.StatusConflict, `{"error":"`+code+`","message":"x"}`)
		_, err := NewInventoryClient(client).Reserve(context.Background(), "PROD_0001", 1)
		assert.ErrorIs(t, err, want)
	}
}

func TestPaymentClient(t *testing.T) {
	client, received := peerServer(t, http.StatusOK, `{"payment_id":1,"order_id":9,"amount":"99.98","status":"SUCCESS"}`)

	err := NewPaymentClient(client).Charge(context.Background(), 9, decimal.RequireFromString("99.98"))

	require.NoError(t, err)
	assert.Equal(t, "POST /payments/process", (*received)["_path"])
	assert.Equal(t, "99.98", (*received)["amount"], "money travels as a fixed-point string")

	client, _ = peerServer(t, http.StatusPaymentRequired,
		`{"error":"PAYMENT_DECLINED","message":"declined","payment":{"status":"DECLINED","decline_reason":"FRAUD_SUSPECTED"}}`)
	err = NewPaymentClient(client).Charge(context.Background(), 9, decimal.NewFromInt(1))
	assert.ErrorIs(t, err, ErrPaymentDeclined)

	client, _ = peerServer(t, http.StatusConflict, `{"error":"ALREADY_PAID","message":"paid"}`)
	err = NewPaymentClient(client).Charge(context.Background(), 9, decimal.NewFromInt(1))
	assert.NoError(t, err, "an order that is already paid counts as paid")
}

func TestMarketingClient(t *testing.T) {
	client, received := peerServer(t, http.StatusOK, `{"attribution_id":99}`)

	err := NewMarketingClient(client).RecordConversion(context.Background(), 99, 9, decimal.RequireFromString("150"))

	require.NoError(t, err)
	assert.Equal(t, "PUT /attributions/99/order", (*received)["_path"])
	assert.EqualValues(t, 9, (*received)["order_id"])
	assert.Equal(t, "150.00", (*received)["order_amount"])
}

func TestShipmentClient(t *testing.T) {
	client, received := peerServer(t, http.StatusAccepted,
		`{"shipment_id":3,"order_id":9,"carrier":"UPS","tracking_number":"1Z999","status":"PENDING","destination_country":"US"}`)

	tracking, err := NewShipmentClient(client).Create(context.Background(), 9, Customer{Country: "US", LoyaltyTier: "GOLD"})

	require.NoError(t, err)
	assert.Equal(t, "1Z999", tracking)
	assert.Equal(t, "POST /shipments/create", (*received)["_path"])
	assert.Equal(t, "US", (*received)["destination_country"])
	assert.Equal(t, "GOLD", (*received)["loyalty_tier"])
}
