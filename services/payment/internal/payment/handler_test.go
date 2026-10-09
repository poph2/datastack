package payment

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
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// fakeStore is an in-memory Store with the same contract as Repository,
// including the at-most-one-SUCCESS-per-order rule.
type fakeStore struct {
	payments map[uint64]Payment
	nextID   uint64
}

func newFakeStore() *fakeStore {
	return &fakeStore{payments: map[uint64]Payment{}, nextID: 1}
}

func (s *fakeStore) Create(_ context.Context, p *Payment) error {
	if p.Status == StatusSuccess {
		for _, existing := range s.payments {
			if existing.OrderID == p.OrderID && existing.Status == StatusSuccess {
				return ErrAlreadyPaid
			}
		}
	}
	p.ID = s.nextID
	s.nextID++
	s.payments[p.ID] = *p
	return nil
}

func (s *fakeStore) Get(_ context.Context, id uint64) (Payment, error) {
	p, ok := s.payments[id]
	if !ok {
		return Payment{}, ErrNotFound
	}
	return p, nil
}

func (s *fakeStore) ListByOrder(_ context.Context, orderID uint64, status Status) ([]Payment, error) {
	var payments []Payment
	for id := uint64(1); id < s.nextID; id++ {
		p, ok := s.payments[id]
		if ok && p.OrderID == orderID && (status == "" || p.Status == status) {
			payments = append(payments, p)
		}
	}
	return payments, nil
}

func (s *fakeStore) Refund(_ context.Context, id uint64) (Payment, error) {
	p, ok := s.payments[id]
	if !ok {
		return Payment{}, ErrNotFound
	}
	if p.Status != StatusSuccess {
		return Payment{}, ErrNotRefundable
	}
	p.Status = StatusRefunded
	s.payments[id] = p
	return p, nil
}

// newTestGateway returns a gateway with no latency that always approves, or
// always declines with INSUFFICIENT_FUNDS.
func newTestGateway(t *testing.T, decline bool) *Gateway {
	t.Helper()
	gateway, err := NewGateway(GatewayConfig{FailureRate: 0.5})
	require.NoError(t, err)

	gateway.float64 = func() float64 {
		if decline {
			return 0
		}
		return 0.99
	}
	gateway.int64N = func(int64) int64 { return 1 }
	return gateway
}

func newTestRouter(t *testing.T, store Store, gateway *Gateway) *gin.Engine {
	t.Helper()

	methods, err := ParseMethodPicker("PAYPAL=1")
	require.NoError(t, err)

	handler := NewHandler(store, gateway, methods)
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "payment-service-test",
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

func TestProcessPaymentSuccess(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store, newTestGateway(t, false))

	rec := doRequest(router, http.MethodPost, "/payments/process",
		`{"order_id": 123, "amount": "99.99", "method": "CREDIT_CARD"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[map[string]any](t, rec)
	assert.EqualValues(t, 1, got["payment_id"])
	assert.EqualValues(t, 123, got["order_id"])
	assert.Equal(t, "99.99", got["amount"])
	assert.Equal(t, "CREDIT_CARD", got["method"])
	assert.Equal(t, "SUCCESS", got["status"])
	assert.NotContains(t, got, "decline_reason")
	assert.Equal(t, StatusSuccess, store.payments[1].Status)
}

func TestProcessPaymentAcceptsNumericAmountAndPicksMethod(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))

	rec := doRequest(router, http.MethodPost, "/payments/process", `{"order_id": 7, "amount": 50}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[paymentResponse](t, rec)
	assert.Equal(t, "50.00", got.Amount, "money always renders with two decimals")
	assert.Equal(t, MethodPayPal, got.Method)
}

func TestProcessPaymentDeclined(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store, newTestGateway(t, true))

	rec := doRequest(router, http.MethodPost, "/payments/process",
		`{"order_id": 123, "amount": "99.99", "method": "CREDIT_CARD"}`)

	require.Equal(t, http.StatusPaymentRequired, rec.Code, rec.Body.String())
	var body struct {
		Error   string         `json:"error"`
		Message string         `json:"message"`
		Payment map[string]any `json:"payment"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, codePaymentDeclined, body.Error)
	assert.NotEmpty(t, body.Message)
	assert.EqualValues(t, 1, body.Payment["payment_id"])
	assert.EqualValues(t, 123, body.Payment["order_id"])
	assert.Equal(t, "99.99", body.Payment["amount"])
	assert.Equal(t, "DECLINED", body.Payment["status"])
	assert.Equal(t, "INSUFFICIENT_FUNDS", body.Payment["decline_reason"])

	require.Contains(t, store.payments, uint64(1), "declines are recorded")
	assert.Equal(t, StatusDeclined, store.payments[1].Status)
}

func TestProcessPaymentAlreadyPaid(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))
	body := `{"order_id": 123, "amount": "10.00"}`
	require.Equal(t, http.StatusOK, doRequest(router, http.MethodPost, "/payments/process", body).Code)

	rec := doRequest(router, http.MethodPost, "/payments/process", body)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, codeAlreadyPaid, decode[httpserver.ErrorResponse](t, rec).Error)
}

func TestProcessPaymentRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"malformed json":       `{"order_id":`,
		"missing order id":     `{"amount": "10.00"}`,
		"zero order id":        `{"order_id": 0, "amount": "10.00"}`,
		"negative order id":    `{"order_id": -1, "amount": "10.00"}`,
		"missing amount":       `{"order_id": 1}`,
		"zero amount":          `{"order_id": 1, "amount": "0"}`,
		"negative amount":      `{"order_id": 1, "amount": "-5.00"}`,
		"three decimal places": `{"order_id": 1, "amount": "9.999"}`,
		"non-numeric amount":   `{"order_id": 1, "amount": "lots"}`,
		"amount too large":     `{"order_id": 1, "amount": "100000000.00"}`,
		"unknown method":       `{"order_id": 1, "amount": "10.00", "method": "BITCOIN"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			router := newTestRouter(t, store, newTestGateway(t, false))

			rec := doRequest(router, http.MethodPost, "/payments/process", body)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Equal(t, httpserver.CodeInvalidRequest, decode[httpserver.ErrorResponse](t, rec).Error)
			assert.Empty(t, store.payments, "invalid requests are not recorded")
		})
	}
}

func TestProcessPaymentAcceptsTrailingZeroes(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))

	rec := doRequest(router, http.MethodPost, "/payments/process", `{"order_id": 1, "amount": "9.900"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "9.90", decode[paymentResponse](t, rec).Amount)
}

func TestGetPayment(t *testing.T) {
	store := newFakeStore()
	store.payments[5] = Payment{OrderID: 9, Amount: decimal.RequireFromString("12.5"), Method: MethodPayPal, Status: StatusSuccess}
	router := newTestRouter(t, store, newTestGateway(t, false))

	rec := doRequest(router, http.MethodGet, "/payments/5", "")

	require.Equal(t, http.StatusOK, rec.Code)
	got := decode[paymentResponse](t, rec)
	assert.Equal(t, uint64(9), got.OrderID)
	assert.Equal(t, "12.50", got.Amount)
}

func TestGetPaymentErrors(t *testing.T) {
	cases := map[string]struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		"unknown id": {"/payments/42", http.StatusNotFound, codePaymentNotFound},
		"zero id":    {"/payments/0", http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"non-number": {"/payments/abc", http.StatusBadRequest, httpserver.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))

			rec := doRequest(router, http.MethodGet, tc.path, "")

			require.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantCode, decode[httpserver.ErrorResponse](t, rec).Error)
		})
	}
}

func TestRefundPayment(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))
	doRequest(router, http.MethodPost, "/payments/process", `{"order_id": 1, "amount": "10.00"}`)

	rec := doRequest(router, http.MethodPost, "/payments/1/refund", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[paymentResponse](t, rec)
	assert.Equal(t, StatusRefunded, got.Status)
	assert.Equal(t, "10.00", got.Amount)

	rec = doRequest(router, http.MethodPost, "/payments/1/refund", "")
	require.Equal(t, http.StatusConflict, rec.Code, "a payment can only be refunded once")
	assert.Equal(t, codeNotRefundable, decode[httpserver.ErrorResponse](t, rec).Error)
}

func TestRefundPaymentErrors(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store, newTestGateway(t, true))
	doRequest(router, http.MethodPost, "/payments/process", `{"order_id": 1, "amount": "10.00"}`)
	require.Equal(t, StatusDeclined, store.payments[1].Status)

	cases := map[string]struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		"declined payment": {"/payments/1/refund", http.StatusConflict, codeNotRefundable},
		"unknown id":       {"/payments/42/refund", http.StatusNotFound, codePaymentNotFound},
		"zero id":          {"/payments/0/refund", http.StatusBadRequest, httpserver.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := doRequest(router, http.MethodPost, tc.path, "")

			require.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantCode, decode[httpserver.ErrorResponse](t, rec).Error)
		})
	}
}

func TestPaymentMetrics(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, true))

	doRequest(router, http.MethodPost, "/payments/process", `{"order_id": 1, "amount": "10.00", "method": "PAYPAL"}`)

	metrics := doRequest(router, http.MethodGet, "/metrics", "").Body.String()
	assert.Contains(t, metrics, `payments_total{method="PAYPAL",status="DECLINED"}`)
	assert.Contains(t, metrics, `payment_declines_total{reason="INSUFFICIENT_FUNDS"}`)
	assert.Contains(t, metrics, `payments_amount_total{status="DECLINED"}`)
}

func TestProbes(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))

	assert.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, "/healthz", "").Code)
	assert.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, "/readyz", "").Code)
}

func TestListPaymentsByOrder(t *testing.T) {
	store := newFakeStore()
	router := newTestRouter(t, store, newTestGateway(t, false))
	for _, p := range []Payment{
		{OrderID: 9, Amount: decimal.RequireFromString("5"), Method: MethodPayPal, Status: StatusDeclined},
		{OrderID: 9, Amount: decimal.RequireFromString("5"), Method: MethodPayPal, Status: StatusSuccess},
		{OrderID: 10, Amount: decimal.RequireFromString("5"), Method: MethodPayPal, Status: StatusSuccess},
	} {
		require.NoError(t, store.Create(context.Background(), &p))
	}

	rec := doRequest(router, http.MethodGet, "/payments?order_id=9", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	all := decode[listResponse](t, rec).Payments
	require.Len(t, all, 2)
	assert.Equal(t, StatusDeclined, all[0].Status)
	assert.Equal(t, "5.00", all[0].Amount)

	rec = doRequest(router, http.MethodGet, "/payments?order_id=9&status=SUCCESS", "")
	require.Equal(t, http.StatusOK, rec.Code)
	success := decode[listResponse](t, rec).Payments
	require.Len(t, success, 1)
	assert.Equal(t, uint64(2), success[0].PaymentID)

	rec = doRequest(router, http.MethodGet, "/payments?order_id=77", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"payments":[]}`, rec.Body.String(), "no payments is an empty list, not 404")
}

func TestListPaymentsRejectsInvalidQuery(t *testing.T) {
	router := newTestRouter(t, newFakeStore(), newTestGateway(t, false))

	for _, path := range []string{"/payments", "/payments?order_id=0", "/payments?order_id=x", "/payments?order_id=1&status=LOST"} {
		rec := doRequest(router, http.MethodGet, path, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
		assert.Equal(t, httpserver.CodeInvalidRequest, decode[httpserver.ErrorResponse](t, rec).Error)
	}
}
