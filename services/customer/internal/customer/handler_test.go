package customer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// fakeStore is an in-memory Store with the same contract as Repository.
type fakeStore struct {
	customers map[uint64]Customer
	nextID    uint64
}

func newFakeStore() *fakeStore {
	return &fakeStore{customers: map[uint64]Customer{}, nextID: 1}
}

func (s *fakeStore) Create(_ context.Context, c *Customer) error {
	for _, existing := range s.customers {
		if existing.Email == c.Email {
			return ErrEmailExists
		}
	}
	c.ID = s.nextID
	s.nextID++
	s.customers[c.ID] = *c
	return nil
}

func (s *fakeStore) Get(_ context.Context, id uint64) (Customer, error) {
	c, ok := s.customers[id]
	if !ok {
		return Customer{}, ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) Update(_ context.Context, id uint64, changes Changes) (Customer, error) {
	c, ok := s.customers[id]
	if !ok {
		return Customer{}, ErrNotFound
	}
	if changes.LoyaltyTier != nil {
		c.LoyaltyTier = *changes.LoyaltyTier
	}
	if changes.Status != nil {
		c.Status = *changes.Status
	}
	s.customers[id] = c
	return c, nil
}

func (s *fakeStore) Delete(_ context.Context, id uint64) error {
	if _, ok := s.customers[id]; !ok {
		return ErrNotFound
	}
	delete(s.customers, id)
	return nil
}

func newTestRouter(t *testing.T, store Store) *gin.Engine {
	t.Helper()

	tiers, err := ParseTierPicker("SILVER=1")
	require.NoError(t, err)

	handler := NewHandler(store, tiers)
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "customer-service-test",
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

func TestCreateCustomer(t *testing.T) {
	router := newTestRouter(t, newFakeStore())

	rec := doRequest(router, http.MethodPost, "/customers",
		`{"email": "Ada@Example.com", "country": "GB", "loyalty_tier": "GOLD"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	got := decode[customerResponse](t, rec)
	assert.Equal(t, uint64(1), got.ID)
	assert.Equal(t, "ada@example.com", got.Email)
	assert.Equal(t, "GB", got.Country)
	assert.Equal(t, TierGold, got.LoyaltyTier)
	assert.Equal(t, StatusActive, got.Status)
}

func TestCreateCustomerAssignsTierWhenOmitted(t *testing.T) {
	router := newTestRouter(t, newFakeStore())

	rec := doRequest(router, http.MethodPost, "/customers", `{"email": "a@example.com", "country": "US"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, TierSilver, decode[customerResponse](t, rec).LoyaltyTier)
}

func TestCreateCustomerRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"malformed json":  `{"email":`,
		"missing email":   `{"country": "US"}`,
		"invalid email":   `{"email": "not-an-email", "country": "US"}`,
		"unknown country": `{"email": "a@example.com", "country": "XX"}`,
		"unknown tier":    `{"email": "a@example.com", "country": "US", "loyalty_tier": "PLATINUM"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			router := newTestRouter(t, newFakeStore())

			rec := doRequest(router, http.MethodPost, "/customers", body)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, httpserver.CodeInvalidRequest, decode[httpserver.ErrorResponse](t, rec).Error)
		})
	}
}

func TestCreateCustomerRejectsDuplicateEmail(t *testing.T) {
	router := newTestRouter(t, newFakeStore())
	body := `{"email": "a@example.com", "country": "US"}`
	require.Equal(t, http.StatusCreated, doRequest(router, http.MethodPost, "/customers", body).Code)

	rec := doRequest(router, http.MethodPost, "/customers", body)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, codeEmailExists, decode[httpserver.ErrorResponse](t, rec).Error)
}

func TestGetCustomer(t *testing.T) {
	router := newTestRouter(t, newFakeStore())
	doRequest(router, http.MethodPost, "/customers", `{"email": "a@example.com", "country": "US"}`)

	rec := doRequest(router, http.MethodGet, "/customers/1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "a@example.com", decode[customerResponse](t, rec).Email)
}

func TestGetCustomerErrors(t *testing.T) {
	cases := map[string]struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		"unknown id": {"/customers/42", http.StatusNotFound, codeCustomerNotFound},
		"zero id":    {"/customers/0", http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"non-number": {"/customers/abc", http.StatusBadRequest, httpserver.CodeInvalidRequest},
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

func TestUpdateCustomer(t *testing.T) {
	router := newTestRouter(t, newFakeStore())
	doRequest(router, http.MethodPost, "/customers", `{"email": "a@example.com", "country": "US"}`)

	rec := doRequest(router, http.MethodPatch, "/customers/1", `{"status": "SUSPENDED", "loyalty_tier": "GOLD"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[customerResponse](t, rec)
	assert.Equal(t, StatusSuspended, got.Status)
	assert.Equal(t, TierGold, got.LoyaltyTier)
}

func TestUpdateCustomerRejectsEmptyOrInvalidChanges(t *testing.T) {
	for name, body := range map[string]string{
		"no fields":      `{}`,
		"unknown status": `{"status": "BANNED"}`,
	} {
		t.Run(name, func(t *testing.T) {
			router := newTestRouter(t, newFakeStore())
			doRequest(router, http.MethodPost, "/customers", `{"email": "a@example.com", "country": "US"}`)

			rec := doRequest(router, http.MethodPatch, "/customers/1", body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestDeleteCustomer(t *testing.T) {
	router := newTestRouter(t, newFakeStore())
	doRequest(router, http.MethodPost, "/customers", `{"email": "a@example.com", "country": "US"}`)

	require.Equal(t, http.StatusNoContent, doRequest(router, http.MethodDelete, "/customers/1", "").Code)
	assert.Equal(t, http.StatusNotFound, doRequest(router, http.MethodGet, "/customers/1", "").Code)
	assert.Equal(t, http.StatusNotFound, doRequest(router, http.MethodDelete, "/customers/1", "").Code)
}

func TestProbesAndMetrics(t *testing.T) {
	router := newTestRouter(t, newFakeStore())

	assert.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, "/healthz", "").Code)
	assert.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, "/readyz", "").Code)

	doRequest(router, http.MethodGet, "/customers/42", "")
	metrics := doRequest(router, http.MethodGet, "/metrics", "").Body.String()
	assert.Contains(t, metrics, `http_server_requests_total{method="GET",route="/customers/:id",status="404"}`)
}
