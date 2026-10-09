package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type echo struct {
	Value string `json:"value"`
}

func TestDoSendsAndDecodesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/things", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var in echo
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		_ = json.NewEncoder(w).Encode(echo{Value: in.Value + "!"})
	}))
	defer server.Close()

	var out echo
	err := New("peer", server.URL+"/", time.Second).Do(context.Background(), http.MethodPost, "/things", echo{Value: "hi"}, &out)

	require.NoError(t, err)
	assert.Equal(t, "hi!", out.Value)
}

func TestDoReturnsAPIErrorWithCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"OUT_OF_STOCK","message":"none left","extra":1}`))
	}))
	defer server.Close()

	err := New("inventory", server.URL, time.Second).Do(context.Background(), http.MethodPost, "/reserve", nil, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.StatusCode)
	assert.Equal(t, "OUT_OF_STOCK", apiErr.Code)
	assert.Equal(t, "none left", apiErr.Message)
	assert.Contains(t, string(apiErr.Body), `"extra":1`)
	assert.Equal(t, "OUT_OF_STOCK", ErrorCode(fmtWrap(err)))
}

func TestDoHandlesNonJSONErrorBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()

	err := New("peer", server.URL, time.Second).Do(context.Background(), http.MethodGet, "/", nil, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Empty(t, apiErr.Code)
}

func TestDoTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	err := New("slow", server.URL, 20*time.Millisecond).Do(context.Background(), http.MethodGet, "/", nil, nil)

	require.Error(t, err)
	assert.Empty(t, ErrorCode(err), "a timeout is a transport error, not an API error")
}

func TestDoPropagatesTraceContext(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	provider := sdktrace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()

	var traceparent string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
	}))
	defer server.Close()

	ctx, span := provider.Tracer("test").Start(context.Background(), "checkout")
	defer span.End()

	require.NoError(t, New("peer", server.URL, time.Second).Do(ctx, http.MethodGet, "/", nil, nil))
	assert.Contains(t, traceparent, span.SpanContext().TraceID().String())
}

func fmtWrap(err error) error {
	return errors.Join(errors.New("context"), err)
}
