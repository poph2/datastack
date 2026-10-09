package analyzer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// fakeAsking returns a canned answer or error.
type fakeAsking struct {
	generator Generator
	answer    *Answer
	err       error
	question  string
	model     string
}

func (f *fakeAsking) Ask(_ context.Context, question, model string) (*Answer, error) {
	f.question, f.model = question, model
	return f.answer, f.err
}

func (f *fakeAsking) Generator() Generator { return f.generator }

func serveRequest(t *testing.T, asker Asking, schemas Schemas, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    "analyzer-test",
		Logger:         discardLogger,
		Ready:          func(context.Context) error { return nil },
		RegisterRoutes: NewHandler(asker, schemas).RegisterRoutes,
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestGetModels(t *testing.T) {
	asker := &fakeAsking{generator: &fakeGenerator{models: []string{"default-model", "other"}}}

	rec := serveRequest(t, asker, loadedSchemas(), http.MethodGet, "/models", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"provider":"fake","default":"default-model","models":["default-model","other"]}`, rec.Body.String())
}

func TestGetModelsWhenProviderIsDown(t *testing.T) {
	asker := &fakeAsking{generator: &fakeGenerator{err: errors.New("llm unavailable: connection refused")}}

	rec := serveRequest(t, asker, loadedSchemas(), http.MethodGet, "/models", "")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.JSONEq(t, `{"error":"LLM_UNAVAILABLE","message":"llm unavailable: connection refused"}`, rec.Body.String())
}

func TestGetSchema(t *testing.T) {
	tables := []Table{{
		Name: "orders", Comment: "Checkouts.",
		Columns: []Column{{Name: "user_id", Type: "bigint(20)", Comment: "Buyer. [ref: customers.id]"}},
	}}
	schemas := staticSchemas{schema: NewSchema("analytics_db", tables, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))}

	rec := serveRequest(t, &fakeAsking{}, schemas, http.MethodGet, "/schema", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"tables": [{"name": "orders", "comment": "Checkouts.",
		            "columns": [{"name": "user_id", "type": "bigint", "comment": "Buyer. [ref: customers.id]"}]}],
		"joins": [{"from": "orders.user_id", "to": "customers.id"}],
		"loaded_at": "2026-10-09T12:00:00Z"
	}`, rec.Body.String())
}

func TestGetSchemaBeforeLoad(t *testing.T) {
	rec := serveRequest(t, &fakeAsking{}, staticSchemas{err: ErrSchemaNotLoaded}, http.MethodGet, "/schema", "")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), `"error":"SCHEMA_NOT_LOADED"`)
}

func TestPostAsk(t *testing.T) {
	asker := &fakeAsking{answer: &Answer{
		Question: "Revenue by tier?",
		Model:    "default-model",
		SQL:      goodSQL,
		Result:   &Result{Columns: oneRowResult().Columns, Rows: [][]any{{"GOLD", "123.40"}}, Truncated: true},
		Attempts: 2,
		Duration: 1500 * time.Millisecond,
	}}

	rec := serveRequest(t, asker, loadedSchemas(), http.MethodPost, "/ask", `{"question":"  Revenue by tier? ","model":"default-model"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Revenue by tier?", asker.question)
	assert.Equal(t, "default-model", asker.model)
	assert.JSONEq(t, `{
		"question": "Revenue by tier?", "model": "default-model", "sql": "`+goodSQL+`",
		"columns": [{"name": "loyalty_tier", "type": "varchar"}, {"name": "revenue", "type": "decimal"}],
		"rows": [["GOLD", "123.40"]], "row_count": 1, "truncated": true,
		"attempts": 2, "duration_ms": 1500, "trace_id": ""
	}`, rec.Body.String())
}

func TestPostAskInvalidRequests(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `{`,
		"empty":          `{}`,
		"blank question": `{"question":"   "}`,
		"too long":       `{"question":"` + strings.Repeat("a", maxQuestionLength+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := serveRequest(t, &fakeAsking{}, loadedSchemas(), http.MethodPost, "/ask", body)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), `"error":"INVALID_REQUEST"`)
		})
	}
}

func TestPostAskErrors(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
		body   string
	}{
		"unknown model": {
			err:    errors.Join(ErrUnknownModel, errors.New("choose one of a, b")),
			status: http.StatusBadRequest,
		},
		"query failed": {
			err:    &AskError{Code: CodeQueryFailed, Message: "Unknown column 'x'", SQL: "SELECT x FROM t", Attempts: 2},
			status: http.StatusUnprocessableEntity,
			body:   `{"error":"QUERY_FAILED","message":"Unknown column 'x'","sql":"SELECT x FROM t"}`,
		},
		"unsafe": {
			err:    &AskError{Code: CodeUnsafeSQL, Message: "unsafe SQL: keyword DELETE is not allowed", SQL: "DELETE FROM t"},
			status: http.StatusUnprocessableEntity,
			body:   `{"error":"UNSAFE_SQL","message":"unsafe SQL: keyword DELETE is not allowed","sql":"DELETE FROM t"}`,
		},
		"no sql": {
			err:    &AskError{Code: CodeNoSQL, Message: ErrNoSQL.Error()},
			status: http.StatusUnprocessableEntity,
			body:   `{"error":"NO_SQL","message":"no SQL query found in the model's reply","sql":""}`,
		},
		"llm down": {
			err:    &AskError{Code: CodeLLMUnavailable, Message: "llm unavailable: timeout"},
			status: http.StatusBadGateway,
			body:   `{"error":"LLM_UNAVAILABLE","message":"llm unavailable: timeout"}`,
		},
		"schema not loaded": {
			err:    ErrSchemaNotLoaded,
			status: http.StatusServiceUnavailable,
			body:   `{"error":"SCHEMA_NOT_LOADED","message":"schema not loaded yet"}`,
		},
		"unexpected": {
			err:    errors.New("boom"),
			status: http.StatusInternalServerError,
			body:   `{"error":"INTERNAL_ERROR","message":"internal server error"}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serveRequest(t, &fakeAsking{err: tt.err}, loadedSchemas(), http.MethodPost, "/ask", `{"question":"q"}`)

			require.Equal(t, tt.status, rec.Code)
			if tt.body != "" {
				assert.JSONEq(t, tt.body, rec.Body.String())
			}
		})
	}
}
