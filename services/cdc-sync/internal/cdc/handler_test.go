package cdc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

func TestStatusEndpoint(t *testing.T) {
	orders := TableRef{Database: "orders", Table: "orders"}
	customers := TableRef{Database: "customers", Table: "customers"}
	tracker := NewTracker([]TableRef{orders, customers})

	cursor := Cursor{UpdatedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), ID: 42}
	tracker.recordLoad(orders, cursor, 10, time.Date(2026, 10, 8, 12, 0, 3, 0, time.UTC))
	tracker.setLag(orders, 1500*time.Millisecond)
	tracker.recordError(customers, errors.New("source table does not exist"))

	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "cdc-sync-test",
		Logger:         discardLogger,
		Ready:          func(context.Context) error { return nil },
		RegisterRoutes: NewStatusHandler(tracker).RegisterRoutes,
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[
		{"table": "orders.orders", "cursor_updated_at": "2026-10-08T12:00:00Z", "cursor_id": 42,
		 "rows_synced": 10, "last_success_at": "2026-10-08T12:00:03Z", "last_error": "", "lag_seconds": 1.5},
		{"table": "customers.customers", "cursor_updated_at": "0001-01-01T00:00:00Z", "cursor_id": 0,
		 "rows_synced": 0, "last_success_at": null, "last_error": "source table does not exist", "lag_seconds": 0}
	]`, rec.Body.String())
}
