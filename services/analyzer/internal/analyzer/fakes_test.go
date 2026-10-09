package analyzer

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeGenerator replies with scripted answers, one per call, and records
// every request.
type fakeGenerator struct {
	mu       sync.Mutex
	replies  []string
	err      error
	models   []string
	requests []GenerateRequest
}

func (g *fakeGenerator) Provider() string     { return "fake" }
func (g *fakeGenerator) DefaultModel() string { return "default-model" }

func (g *fakeGenerator) Models(context.Context) ([]string, error) {
	if g.err != nil {
		return nil, g.err
	}
	return g.models, nil
}

func (g *fakeGenerator) Generate(_ context.Context, req GenerateRequest) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.requests = append(g.requests, req)
	if g.err != nil {
		return "", g.err
	}
	reply := g.replies[0]
	if len(g.replies) > 1 {
		g.replies = g.replies[1:]
	}
	return reply, nil
}

// fakeDatabase fails EXPLAIN or execution for the queries listed, and
// returns result for everything else.
type fakeDatabase struct {
	explainErrors map[string]error
	queryErrors   map[string]error
	result        *Result
	executed      []string
}

func (d *fakeDatabase) Explain(_ context.Context, query string) error {
	return d.explainErrors[query]
}

func (d *fakeDatabase) Query(_ context.Context, query string, _ int) (*Result, error) {
	d.executed = append(d.executed, query)
	if err := d.queryErrors[query]; err != nil {
		return nil, err
	}
	return d.result, nil
}

// staticSchemas always returns the same schema (or error).
type staticSchemas struct {
	schema *Schema
	err    error
}

func (s staticSchemas) Current() (*Schema, error) { return s.schema, s.err }

func loadedSchemas() staticSchemas {
	return staticSchemas{schema: NewSchema("analytics_db", testTables(), time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))}
}

func oneRowResult() *Result {
	return &Result{
		Columns: []ResultColumn{{Name: "loyalty_tier", Type: "varchar"}, {Name: "revenue", Type: "decimal"}},
		Rows:    [][]any{{"GOLD", "123.40"}},
	}
}
