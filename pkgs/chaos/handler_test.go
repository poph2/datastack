package chaos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/poph2/datastack/pkgs/httpserver"
)

const pingPath = "/ping"

// newTestRouter serves store's admin endpoints, one extra admin route, and a
// /ping API route, through the real shared router.
func newTestRouter(store *Store) *gin.Engine {
	api := func(r gin.IRouter) {
		r.GET(pingPath, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"pong": true}) })
	}
	extra := func(r gin.IRouter) {
		r.POST(AdminPath+"/action", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"done": true}) })
	}
	return httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "chaos-test",
		Logger:         discardLogger,
		Ready:          func(context.Context) error { return nil },
		RegisterRoutes: store.Routes(api, extra),
	})
}

func doRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) statusResponse {
	t.Helper()
	var body statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func TestGetChaos(t *testing.T) {
	router := newTestRouter(newTestStore(t, newRateKnob(t)))

	rec := doRequest(router, http.MethodGet, AdminPath, "")

	require.Equal(t, http.StatusOK, rec.Code)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.Equal(t, "test-service", raw["service"])
	knobs := raw["knobs"].([]any)
	require.Len(t, knobs, 2)
	first := knobs[0].(map[string]any)
	assert.Equal(t, LatencyKnob, first["name"])
	for _, key := range []string{"description", "value", "default", "min", "max"} {
		assert.Contains(t, first, key)
	}
}

func TestPutChaosAppliesPartialUpdate(t *testing.T) {
	rate := newRateKnob(t)
	router := newTestRouter(newTestStore(t, rate))

	rec := doRequest(router, http.MethodPut, AdminPath, `{"decline_rate": 0.5}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeStatus(t, rec)
	assert.InDelta(t, 0, body.Knobs[0].Value, 0, "latency is untouched")
	assert.InDelta(t, 0.5, body.Knobs[1].Value, 0)
	assert.InDelta(t, 0.5, rate.Value(), 0)
}

func TestPutChaosRejectsInvalidChanges(t *testing.T) {
	cases := map[string]string{
		"unknown knob":   `{"latency_ms": 10, "nope": 1}`,
		"out of range":   `{"latency_ms": 10, "decline_rate": 2}`,
		"not a number":   `{"latency_ms": "fast"}`,
		"not an object":  `[1, 2]`,
		"empty body":     ``,
		"malformed json": `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := newTestStore(t, newRateKnob(t))
			router := newTestRouter(store)

			rec := doRequest(router, http.MethodPut, AdminPath, body)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			var errBody httpserver.ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errBody))
			assert.Equal(t, httpserver.CodeInvalidRequest, errBody.Error)
			assert.NotEmpty(t, errBody.Message)
			assert.Zero(t, store.Latency(), "nothing is applied")
		})
	}
}

func TestDeleteChaosResets(t *testing.T) {
	store := newTestStore(t, newRateKnob(t))
	router := newTestRouter(store)
	require.Equal(t, http.StatusOK, doRequest(router, http.MethodPut, AdminPath, `{"latency_ms": 100, "decline_rate": 1}`).Code)

	rec := doRequest(router, http.MethodDelete, AdminPath, "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeStatus(t, rec)
	assert.InDelta(t, 0, body.Knobs[0].Value, 0)
	assert.InDelta(t, 0.05, body.Knobs[1].Value, 0)
}

func TestLatencyAppliesToAPIRoutesOnly(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	require.NoError(t, store.Apply(context.Background(), map[string]float64{LatencyKnob: 100}))

	for _, path := range []string{AdminPath, AdminPath + "/action"} {
		method := http.MethodGet
		if path != AdminPath {
			method = http.MethodPost
		}
		start := time.Now()
		rec := doRequest(router, method, path, "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Less(t, time.Since(start), 50*time.Millisecond, "%s is never delayed", path)
	}

	start := time.Now()
	rec := doRequest(router, http.MethodGet, pingPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
}

func TestLatencyIsRecordedOnTheRequestSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	store := newTestStore(t)
	router := newTestRouter(store)
	require.NoError(t, store.Apply(context.Background(), map[string]float64{LatencyKnob: 10}))

	require.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, pingPath, "").Code)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Attributes(), attribute.Int64("chaos.latency_ms", 10))
	var events []string
	for _, event := range spans[0].Events() {
		events = append(events, event.Name)
	}
	assert.Equal(t, []string{"chaos.latency.start", "chaos.latency.end"}, events)
}

func TestLatencyEndsWhenTheCallerGivesUp(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	require.NoError(t, store.Apply(context.Background(), map[string]float64{LatencyKnob: 5000}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, pingPath, nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	router.ServeHTTP(rec, req)

	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "pong", "the handler never runs")
}
