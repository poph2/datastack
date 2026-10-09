package marketing

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// fakeStore is an in-memory Store with the same contract as Repository.
type fakeStore struct {
	campaigns    []Campaign
	attributions map[uint64]Attribution
	nextID       uint64
}

func newFakeStore(campaigns ...Campaign) *fakeStore {
	return &fakeStore{campaigns: campaigns, attributions: map[uint64]Attribution{}, nextID: 1}
}

func (s *fakeStore) ListCampaigns(context.Context) ([]Campaign, error) {
	return s.campaigns, nil
}

func (s *fakeStore) CreateAttribution(_ context.Context, a *Attribution) error {
	if !s.hasCampaign(a.UTMSource, a.UTMCampaign) {
		return ErrCampaignNotFound
	}
	a.ID = s.nextID
	s.nextID++
	s.attributions[a.ID] = *a
	return nil
}

func (s *fakeStore) GetAttribution(_ context.Context, id uint64) (Attribution, error) {
	a, ok := s.attributions[id]
	if !ok {
		return Attribution{}, ErrAttributionNotFound
	}
	return a, nil
}

func (s *fakeStore) Convert(_ context.Context, id uint64, conversion Conversion) (ConvertResult, error) {
	a, ok := s.attributions[id]
	switch {
	case !ok:
		return ConvertResult{}, ErrAttributionNotFound
	case a.Converted() && *a.OrderID == conversion.OrderID:
		return ConvertResult{Attribution: a}, nil
	case a.Converted():
		return ConvertResult{}, ErrAlreadyConverted
	}

	now := time.Now()
	a.OrderID, a.OrderAmount, a.ConvertedAt = &conversion.OrderID, &conversion.OrderAmount, &now
	s.attributions[id] = a
	return ConvertResult{Attribution: a, FirstConversion: true}, nil
}

func (s *fakeStore) hasCampaign(source, campaign string) bool {
	for _, c := range s.campaigns {
		if c.UTMSource == source && c.UTMCampaign == campaign {
			return true
		}
	}
	return false
}

func campaign(id uint64, source, name, adSpend string) Campaign {
	c := Campaign{UTMSource: source, UTMCampaign: name, AdSpend: decimal.RequireFromString(adSpend)}
	c.ID = id
	return c
}

func newSeededFakeStore() *fakeStore {
	return newFakeStore(
		campaign(1, "facebook", "summer_sale", "800"),
		campaign(2, "email", "newsletter", "0"),
	)
}

func newTestRouter(store Store) *gin.Engine {
	handler := NewHandler(store)
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "marketing-service-test",
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

// createAttribution logs a facebook/summer_sale touchpoint and returns its ID.
func createAttribution(t *testing.T, router http.Handler) uint64 {
	t.Helper()
	rec := doRequest(router, http.MethodPost, "/attributions",
		`{"user_id": 1234, "utm_source": "facebook", "utm_campaign": "summer_sale"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return decode[createdAttributionResponse](t, rec).AttributionID
}

func TestListCampaignsFormatsMoney(t *testing.T) {
	router := newTestRouter(newSeededFakeStore())

	rec := doRequest(router, http.MethodGet, "/campaigns", "")

	require.Equal(t, http.StatusOK, rec.Code)
	got := decode[[]campaignResponse](t, rec)
	require.Len(t, got, 2)
	assert.Equal(t, uint64(1), got[0].CampaignID)
	assert.Equal(t, "facebook", got[0].UTMSource)
	assert.Equal(t, "summer_sale", got[0].UTMCampaign)
	assert.Equal(t, "800.00", got[0].AdSpend, "money always renders with two decimals")
	assert.Equal(t, "0.00", got[1].AdSpend)
}

func TestCreateAttribution(t *testing.T) {
	store := newSeededFakeStore()
	router := newTestRouter(store)

	rec := doRequest(router, http.MethodPost, "/attributions",
		`{"user_id": 1234, "utm_source": "facebook", "utm_campaign": "summer_sale"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{
		"attribution_id": 1,
		"user_id": 1234,
		"utm_source": "facebook",
		"utm_campaign": "summer_sale",
		"created_at": "0001-01-01T00:00:00Z"
	}`, rec.Body.String())
	assert.Len(t, store.attributions, 1)
}

func TestCreateAttributionFailures(t *testing.T) {
	long := strings.Repeat("x", 101)
	cases := map[string]struct {
		body       string
		wantStatus int
		wantCode   string
	}{
		"unknown campaign":    {`{"user_id": 1, "utm_source": "facebook", "utm_campaign": "nope"}`, http.StatusNotFound, codeCampaignNotFound},
		"unknown source":      {`{"user_id": 1, "utm_source": "myspace", "utm_campaign": "summer_sale"}`, http.StatusNotFound, codeCampaignNotFound},
		"missing user":        {`{"utm_source": "facebook", "utm_campaign": "summer_sale"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"zero user":           {`{"user_id": 0, "utm_source": "facebook", "utm_campaign": "summer_sale"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"negative user":       {`{"user_id": -1, "utm_source": "facebook", "utm_campaign": "summer_sale"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"missing source":      {`{"user_id": 1, "utm_campaign": "summer_sale"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"missing campaign":    {`{"user_id": 1, "utm_source": "facebook"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"source too long":     {`{"user_id": 1, "utm_source": "` + long + `", "utm_campaign": "summer_sale"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"campaign too long":   {`{"user_id": 1, "utm_source": "facebook", "utm_campaign": "` + long + `"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"malformed json body": {`{"user_id": `, http.StatusBadRequest, httpserver.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newSeededFakeStore()
			router := newTestRouter(store)

			rec := doRequest(router, http.MethodPost, "/attributions", tc.body)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantCode, decode[httpserver.ErrorResponse](t, rec).Error)
			assert.Empty(t, store.attributions)
		})
	}
}

func TestGetAttribution(t *testing.T) {
	router := newTestRouter(newSeededFakeStore())
	createAttribution(t, router)

	rec := doRequest(router, http.MethodGet, "/attributions/1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"attribution_id": 1,
		"user_id": 1234,
		"utm_source": "facebook",
		"utm_campaign": "summer_sale",
		"order_id": null,
		"order_amount": null,
		"converted_at": null,
		"created_at": "0001-01-01T00:00:00Z",
		"updated_at": "0001-01-01T00:00:00Z"
	}`, rec.Body.String(), "an unconverted attribution has null conversion fields")

	rec = doRequest(router, http.MethodGet, "/attributions/999", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, codeAttributionNotFound, decode[httpserver.ErrorResponse](t, rec).Error)

	assert.Equal(t, http.StatusBadRequest, doRequest(router, http.MethodGet, "/attributions/0", "").Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(router, http.MethodGet, "/attributions/abc", "").Code)
}

func TestConvert(t *testing.T) {
	router := newTestRouter(newSeededFakeStore())
	createAttribution(t, router)

	rec := doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 999, "order_amount": "150.00"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[attributionResponse](t, rec)
	assert.Equal(t, uint64(1), got.AttributionID)
	require.NotNil(t, got.OrderID)
	assert.Equal(t, uint64(999), *got.OrderID)
	require.NotNil(t, got.OrderAmount)
	assert.Equal(t, "150.00", *got.OrderAmount)
	assert.NotNil(t, got.ConvertedAt)
}

func TestConvertAcceptsNumericAmountAndRendersTwoDecimals(t *testing.T) {
	router := newTestRouter(newSeededFakeStore())
	createAttribution(t, router)

	rec := doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 999, "order_amount": 150}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "150.00", *decode[attributionResponse](t, rec).OrderAmount)
}

func TestConvertIsIdempotentAndRejectsADifferentOrder(t *testing.T) {
	router := newTestRouter(newSeededFakeStore())
	createAttribution(t, router)
	require.Equal(t, http.StatusOK,
		doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 999, "order_amount": "150.00"}`).Code)

	rec := doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 999, "order_amount": "150.00"}`)
	require.Equal(t, http.StatusOK, rec.Code, "the same order again is a safe retry")
	assert.Equal(t, uint64(999), *decode[attributionResponse](t, rec).OrderID)

	rec = doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 1000, "order_amount": "150.00"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, codeAlreadyConverted, decode[httpserver.ErrorResponse](t, rec).Error)
}

func TestConvertFailures(t *testing.T) {
	cases := map[string]struct {
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		"unknown attribution":  {"/attributions/999/order", `{"order_id": 1, "order_amount": "10.00"}`, http.StatusNotFound, codeAttributionNotFound},
		"invalid id":           {"/attributions/0/order", `{"order_id": 1, "order_amount": "10.00"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"missing order id":     {"/attributions/1/order", `{"order_amount": "10.00"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"zero order id":        {"/attributions/1/order", `{"order_id": 0, "order_amount": "10.00"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"missing amount":       {"/attributions/1/order", `{"order_id": 1}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"zero amount":          {"/attributions/1/order", `{"order_id": 1, "order_amount": "0.00"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"negative amount":      {"/attributions/1/order", `{"order_id": 1, "order_amount": "-5.00"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"three decimal places": {"/attributions/1/order", `{"order_id": 1, "order_amount": "10.005"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"three dp as number":   {"/attributions/1/order", `{"order_id": 1, "order_amount": 10.005}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"amount too large":     {"/attributions/1/order", `{"order_id": 1, "order_amount": "100000000.00"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
		"amount not a number":  {"/attributions/1/order", `{"order_id": 1, "order_amount": "lots"}`, http.StatusBadRequest, httpserver.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newSeededFakeStore()
			router := newTestRouter(store)
			createAttribution(t, router)

			rec := doRequest(router, http.MethodPut, tc.path, tc.body)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantCode, decode[httpserver.ErrorResponse](t, rec).Error)
			assert.False(t, store.attributions[1].Converted(), "a failed conversion must not change the attribution")
		})
	}
}

func TestConvertAcceptsTrailingZeroes(t *testing.T) {
	router := newTestRouter(newSeededFakeStore())
	createAttribution(t, router)

	rec := doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 1, "order_amount": "10.500"}`)

	require.Equal(t, http.StatusOK, rec.Code, "10.500 is a two-decimal amount written with a trailing zero")
	assert.Equal(t, "10.50", *decode[attributionResponse](t, rec).OrderAmount)
}

func TestConversionMetricsCountOnlyFirstConversion(t *testing.T) {
	router := newTestRouter(newFakeStore(campaign(1, "metrics_src", "metrics_campaign", "10")))

	rec := doRequest(router, http.MethodPost, "/attributions",
		`{"user_id": 1, "utm_source": "metrics_src", "utm_campaign": "metrics_campaign"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	for range 3 {
		rec = doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 7, "order_amount": "150.25"}`)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	rec = doRequest(router, http.MethodPut, "/attributions/1/order", `{"order_id": 8, "order_amount": "99.00"}`)
	require.Equal(t, http.StatusConflict, rec.Code)

	const label = `{utm_source="metrics_src"}`
	assert.InDelta(t, 1, scrapeMetric(t, router, "marketing_attributions_total"+label), 0)
	assert.InDelta(t, 1, scrapeMetric(t, router, "marketing_conversions_total"+label), 0, "retries and conflicts are not conversions")
	assert.InDelta(t, 150.25, scrapeMetric(t, router, "marketing_conversion_revenue_total"+label), 1e-9)
}

// scrapeMetric reads one sample's value from the router's /metrics page.
func scrapeMetric(t *testing.T, router http.Handler, series string) float64 {
	t.Helper()
	page := doRequest(router, http.MethodGet, "/metrics", "").Body.String()
	for line := range strings.Lines(page) {
		value, found := strings.CutPrefix(strings.TrimSpace(line), series+" ")
		if !found {
			continue
		}
		f, err := strconv.ParseFloat(value, 64)
		require.NoError(t, err, line)
		return f
	}
	t.Fatalf("series %s not found on /metrics", series)
	return 0
}
