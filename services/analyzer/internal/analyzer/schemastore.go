package analyzer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ErrSchemaNotLoaded means the schema has never been read: StarRocks is
// down, or analytics_db has no tables yet.
var ErrSchemaNotLoaded = errors.New("schema not loaded yet")

// retryInterval is how soon a failed first load is retried, so the service
// becomes ready shortly after StarRocks does.
const retryInterval = 5 * time.Second

// SchemaSource reads the documented schema and keeps the read-only user's
// grants in place. *StarRocks implements it.
type SchemaSource interface {
	EnsureReadOnlyUser(ctx context.Context) error
	LoadSchema(ctx context.Context) (*Schema, error)
}

// SchemaStore holds the latest schema and refreshes it in the background.
type SchemaStore struct {
	source   SchemaSource
	interval time.Duration
	logger   *slog.Logger

	mu      sync.RWMutex
	current *Schema
}

// NewSchemaStore returns an empty store. Call Refresh or Run to fill it.
func NewSchemaStore(source SchemaSource, interval time.Duration, logger *slog.Logger) *SchemaStore {
	return &SchemaStore{source: source, interval: interval, logger: logger}
}

// Current returns the latest schema, or ErrSchemaNotLoaded.
func (s *SchemaStore) Current() (*Schema, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.current == nil {
		return nil, ErrSchemaNotLoaded
	}
	return s.current, nil
}

// Ready is the readiness check: the service can't answer anything before
// the schema has loaded once.
func (s *SchemaStore) Ready(context.Context) error {
	_, err := s.Current()
	return err
}

// Refresh re-grants the read-only user and reloads the schema. On failure
// the previous schema stays in place: comments rarely change, and a stale
// prompt beats no answer.
func (s *SchemaStore) Refresh(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "analyzer.schema_refresh")
	defer span.End()

	schema, err := s.load(ctx)
	if err != nil {
		schemaRefreshesTotal.WithLabelValues(outcomeFailed).Inc()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetAttributes(attribute.Int("analyzer.tables", len(schema.Tables)), attribute.Int("analyzer.joins", len(schema.Joins)))
	schemaRefreshesTotal.WithLabelValues(outcomeSuccess).Inc()

	s.mu.Lock()
	s.current = schema
	s.mu.Unlock()
	return nil
}

func (s *SchemaStore) load(ctx context.Context) (*Schema, error) {
	if err := s.source.EnsureReadOnlyUser(ctx); err != nil {
		return nil, err
	}
	schema, err := s.source.LoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading schema: %w", err)
	}
	return schema, nil
}

// Run refreshes until ctx ends: every retryInterval until the first load
// succeeds, then every interval.
func (s *SchemaStore) Run(ctx context.Context) {
	for {
		wait := s.interval
		if err := s.Refresh(ctx); err != nil {
			s.logger.WarnContext(ctx, "schema refresh failed", "error", err)
			if _, notLoaded := s.Current(); notLoaded != nil {
				wait = retryInterval
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
