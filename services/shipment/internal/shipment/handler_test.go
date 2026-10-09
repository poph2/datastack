package shipment

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// fakeStore is an in-memory Store with the same contract as Repository.
type fakeStore struct {
	byOrder map[uint64]Shipment
	nextID  uint64
}

func newFakeStore() *fakeStore {
	return &fakeStore{byOrder: map[uint64]Shipment{}, nextID: 1}
}

func (s *fakeStore) CreateOrGet(_ context.Context, shipment Shipment) (Shipment, bool, error) {
	if existing, ok := s.byOrder[shipment.OrderID]; ok {
		return existing, false, nil
	}
	shipment.ID = s.nextID
	s.nextID++
	s.byOrder[shipment.OrderID] = shipment
	return shipment, true, nil
}

func (s *fakeStore) GetByOrderID(_ context.Context, orderID uint64) (Shipment, error) {
	shipment, ok := s.byOrder[orderID]
	if !ok {
		return Shipment{}, ErrNotFound
	}
	return shipment, nil
}

// newFixedPlanner returns a planner whose every random draw is 0: US orders
// go by UPS, tracking numbers are all zeros, and the delay is the minimum.
func newFixedPlanner(t *testing.T) *Planner {
	t.Helper()
	planner, err := NewPlanner(PlanConfig{
		PackingDelayMin: 2 * time.Second,
		PackingDelayMax: 30 * time.Second,
		GoldDelayFactor: 0.5,
	})
	require.NoError(t, err)
	planner.intN = func(int) int { return 0 }
	return planner
}

func newTestRouter(t *testing.T, store Store) *gin.Engine {
	t.Helper()
	handler := NewHandler(store, newFixedPlanner(t))
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "shipment-service-test",
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

func TestCreateShipment(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store)

	rec := doRequest(router, http.MethodPost, "/shipments/create",
		`{"order_id": 123, "destination_country": "US", "loyalty_tier": "GOLD"}`)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{
		"shipment_id": 1,
		"order_id": 123,
		"carrier": "UPS",
		"tracking_number": "1Z0000000000000000",
		"status": "PENDING",
		"destination_country": "US"
	}`, rec.Body.String())
	assert.Equal(t, 1000, store.byOrder[123].PackingDelayMS, "GOLD halves the 2s minimum")
}

func TestCreateShipmentIsIdempotent(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store)
	first := doRequest(router, http.MethodPost, "/shipments/create", `{"order_id": 7, "destination_country": "US"}`)
	require.Equal(t, http.StatusAccepted, first.Code)

	// A retry with a different destination would plan a DHL shipment, but the
	// order already has one, so that one comes back unchanged.
	rec := doRequest(router, http.MethodPost, "/shipments/create", `{"order_id": 7, "destination_country": "DE"}`)

	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.JSONEq(t, first.Body.String(), rec.Body.String())
	assert.Len(t, store.byOrder, 1)
}

func TestCreateShipmentRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"malformed json":      `{"order_id":`,
		"missing order":       `{"destination_country": "US"}`,
		"zero order":          `{"order_id": 0, "destination_country": "US"}`,
		"negative order":      `{"order_id": -1, "destination_country": "US"}`,
		"missing destination": `{"order_id": 1}`,
		"unknown country":     `{"order_id": 1, "destination_country": "XX"}`,
		"alpha-3 country":     `{"order_id": 1, "destination_country": "USA"}`,
		"unknown tier":        `{"order_id": 1, "destination_country": "US", "loyalty_tier": "PLATINUM"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			router := newTestRouter(t, store)

			rec := doRequest(router, http.MethodPost, "/shipments/create", body)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, httpserver.CodeInvalidRequest, decode[httpserver.ErrorResponse](t, rec).Error)
			assert.Empty(t, store.byOrder)
		})
	}
}

func TestGetShipment(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store)
	doRequest(router, http.MethodPost, "/shipments/create", `{"order_id": 5, "destination_country": "US"}`)

	rec := doRequest(router, http.MethodGet, "/shipments/5", "")

	require.Equal(t, http.StatusOK, rec.Code)
	got := decode[map[string]any](t, rec)
	assert.Equal(t, "PENDING", got["status"])
	assert.EqualValues(t, 2000, got["packing_delay_ms"])
	assert.Contains(t, got, "created_at")
	assert.NotContains(t, got, "shipped_at", "shipped_at is omitted until the box ships")

	shippedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	shipped := store.byOrder[5]
	shipped.Status, shipped.ShippedAt = StatusShipped, &shippedAt
	store.byOrder[5] = shipped

	got = decode[map[string]any](t, doRequest(router, http.MethodGet, "/shipments/5", ""))
	assert.Equal(t, "SHIPPED", got["status"])
	assert.Equal(t, "2026-01-02T03:04:05Z", got["shipped_at"])
}

func TestGetShipmentErrors(t *testing.T) {
	cases := map[string]struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		"unknown order": {"/shipments/42", http.StatusNotFound, codeShipmentNotFound},
		"zero order":    {"/shipments/0", http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"non-number":    {"/shipments/abc", http.StatusBadRequest, httpserver.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			router := newTestRouter(t, newFakeStore())

			rec := doRequest(router, http.MethodGet, tc.path, "")

			require.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantCode, decode[httpserver.ErrorResponse](t, rec).Error)
		})
	}
}

func TestCreateShipmentMetrics(t *testing.T) {
	router := newTestRouter(t, newFakeStore())

	doRequest(router, http.MethodPost, "/shipments/create", `{"order_id": 1, "destination_country": "US"}`)

	metrics := doRequest(router, http.MethodGet, "/metrics", "").Body.String()
	assert.Contains(t, metrics, `shipments_created_total{carrier="UPS"}`)
	assert.Contains(t, metrics, `http_server_requests_total{method="POST",route="/shipments/create",status="202"}`)
}
