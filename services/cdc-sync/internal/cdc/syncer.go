package cdc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Postgres error codes for a source that hasn't been created yet.
const (
	pgUndefinedTable  = "42P01"
	pgUnknownDatabase = "3D000"
)

// epoch is the starting cursor for a table StarRocks holds nothing of yet:
// a full initial load.
var epoch = time.Unix(0, 0).UTC()

var tracer = otel.Tracer("cdc-sync")

// Catalog is the StarRocks schema and metadata side of the sync.
// *StarRocksCatalog implements it, and tests fake it.
type Catalog interface {
	// EnsureTable creates the target table, or adds columns it lacks.
	EnsureTable(ctx context.Context, table string, schema TableSchema) error
	// SyncComments brings an existing table's comments in line with schema.
	SyncComments(ctx context.Context, table string, schema TableSchema) (CommentChanges, error)
	// MaxUpdatedAt returns the newest updated_at loaded, zero when empty.
	MaxUpdatedAt(ctx context.Context, table string) (time.Time, error)
}

// SyncConfig tunes every table loop.
type SyncConfig struct {
	// PollInterval is the time between polls once a table is caught up.
	PollInterval time.Duration
	// BatchSize is the most rows read and loaded at once.
	BatchSize int
	// SafetyLag leaves rows changed this recently for a later poll.
	SafetyLag time.Duration
	// SchemaRefreshInterval is how often a running table re-reads its source
	// schema, so changed comments reach StarRocks (and comment changes that
	// StarRocks refused while a schema change ran are retried).
	SchemaRefreshInterval time.Duration
}

func (c SyncConfig) validate() error {
	if c.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive, got %s", c.PollInterval)
	}
	if c.BatchSize < 1 {
		return fmt.Errorf("batch size must be at least 1, got %d", c.BatchSize)
	}
	if c.SafetyLag < 0 {
		return fmt.Errorf("safety lag must not be negative, got %s", c.SafetyLag)
	}
	if c.SchemaRefreshInterval <= 0 {
		return fmt.Errorf("schema refresh interval must be positive, got %s", c.SchemaRefreshInterval)
	}
	return nil
}

// Dependencies are what the table loops read from and write to.
type Dependencies struct {
	Sources *Sources
	Catalog Catalog
	Loader  *StreamLoader
	Tracker *Tracker
	Logger  *slog.Logger
}

// Syncer runs one polling loop per configured table.
type Syncer struct {
	tables []*tableSyncer
}

// NewSyncer validates config and prepares a loop for each of refs.
func NewSyncer(refs []TableRef, deps Dependencies, config SyncConfig) (*Syncer, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}

	syncer := &Syncer{}
	for _, ref := range refs {
		syncer.tables = append(syncer.tables, &tableSyncer{
			ref:    ref,
			deps:   deps,
			config: config,
			logger: deps.Logger.With("table", ref.String()),
		})
	}
	return syncer, nil
}

// Run syncs every table until ctx is cancelled. Tables are independent: one
// that is missing or failing never holds up the others.
func (s *Syncer) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, table := range s.tables {
		wg.Go(func() { table.run(ctx) })
	}
	wg.Wait()
}

// tableSyncer is one table's loop. Its fields after logger are owned by the
// loop's goroutine; the Tracker is how anything else sees its progress.
type tableSyncer struct {
	ref    TableRef
	deps   Dependencies
	config SyncConfig
	logger *slog.Logger

	ready             bool
	cursor            Cursor
	columns           map[string]bool
	setupError        string
	nextSchemaRefresh time.Time
	keyCommentNoted   bool
}

func (t *tableSyncer) run(ctx context.Context) {
	ticker := time.NewTicker(t.config.PollInterval)
	defer ticker.Stop()

	for {
		t.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick sets the table up if it isn't yet (or refreshes its schema when that
// is due), drains every pending change, and refreshes the lag gauge.
func (t *tableSyncer) tick(ctx context.Context) {
	if !t.ready {
		if err := t.setup(ctx); err != nil {
			t.setupFailed(ctx, err)
			return
		}
	} else if !time.Now().Before(t.nextSchemaRefresh) {
		t.refreshSchema(ctx)
	}
	t.drain(ctx)
	t.updateLag(ctx)
}

// setup mirrors the source schema into StarRocks and derives the starting
// cursor from what StarRocks already holds. The cursor starts at id 0 within
// the newest loaded timestamp, so rows sharing that timestamp are re-sent;
// the Primary Key upsert makes that harmless.
func (t *tableSyncer) setup(ctx context.Context) error {
	if err := t.syncSchema(ctx); err != nil {
		return err
	}

	newest, err := t.deps.Catalog.MaxUpdatedAt(ctx, t.ref.Table)
	if err != nil {
		return err
	}
	if newest.IsZero() {
		newest = epoch
	}

	t.cursor = Cursor{UpdatedAt: newest}
	t.ready = true
	t.setupError = ""
	t.deps.Tracker.setCursor(t.ref, t.cursor)
	t.deps.Tracker.clearError(t.ref)
	t.logger.InfoContext(ctx, "table ready to sync", "cursor_updated_at", newest, "columns", len(t.columns))
	return nil
}

// syncSchema mirrors the source columns into StarRocks, then its comments.
// Only the columns can fail it: comments are documentation, and never worth
// holding up a load.
func (t *tableSyncer) syncSchema(ctx context.Context) error {
	t.nextSchemaRefresh = time.Now().Add(t.config.SchemaRefreshInterval)

	schema, err := t.deps.Sources.Schema(ctx, t.ref)
	if err != nil {
		return err
	}
	if err := validateCursorColumns(schema.Columns); err != nil {
		return fmt.Errorf("%s: %w", t.ref, err)
	}
	if err := t.deps.Catalog.EnsureTable(ctx, t.ref.Table, schema); err != nil {
		return err
	}

	t.columns = map[string]bool{}
	for _, c := range schema.Columns {
		t.columns[c.Name] = true
	}
	t.syncComments(ctx, schema)
	return nil
}

// refreshSchema re-mirrors the schema of a table that is already syncing. A
// failure is only logged: rows keep loading, and the next refresh retries.
func (t *tableSyncer) refreshSchema(ctx context.Context) {
	if err := t.syncSchema(ctx); err != nil && ctx.Err() == nil {
		t.logger.WarnContext(ctx, "refreshing schema failed, retrying on the next refresh", "error", err)
	}
}

// syncComments carries comment changes into StarRocks. A schema change still
// running (right after an ADD COLUMN or an earlier comment change) makes
// StarRocks refuse them; that is expected, so it is logged at INFO and left
// to the next refresh like any other failure here.
func (t *tableSyncer) syncComments(ctx context.Context, schema TableSchema) {
	changes, err := t.deps.Catalog.SyncComments(ctx, t.ref.Table, schema)
	t.deps.Tracker.setCommentsInSync(t.ref, err == nil)
	t.noteKeyComment(ctx, changes.KeyCommentDiffers)

	switch {
	case ctx.Err() != nil:
	case isSchemaChangeInProgress(err):
		t.logger.InfoContext(ctx, "schema change in progress, syncing comments on the next refresh",
			"retry_in", t.config.SchemaRefreshInterval)
	case err != nil:
		t.logger.WarnContext(ctx, "syncing comments failed, retrying on the next refresh", "error", err)
	case len(changes.Statements) > 0:
		t.logger.InfoContext(ctx, "comments synced", "statements", len(changes.Statements))
	}
}

// noteKeyComment logs once that id's comment is out of date, rather than on
// every refresh.
func (t *tableSyncer) noteKeyComment(ctx context.Context, differs bool) {
	if differs && !t.keyCommentNoted {
		t.logger.InfoContext(ctx, "id comment differs from Postgres and is left as is: changing a key column would start a full schema change job")
	}
	t.keyCommentNoted = differs
}

// setupFailed reports a table that can't sync yet. That is expected while
// its service or StarRocks is still being deployed, so it is a warning,
// logged once per distinct error rather than on every tick.
func (t *tableSyncer) setupFailed(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	t.deps.Tracker.recordError(t.ref, err)
	if err.Error() == t.setupError {
		return
	}
	t.setupError = err.Error()
	t.logger.WarnContext(ctx, "table not ready to sync, retrying on the next poll", "error", err)
}

// drain loads batches until one comes back short, so a backlog is cleared
// without waiting a poll interval between full batches.
func (t *tableSyncer) drain(ctx context.Context) {
	for ctx.Err() == nil {
		full, err := t.syncBatch(ctx)
		if err != nil {
			t.batchFailed(ctx, err)
			return
		}
		if !full {
			return
		}
	}
}

// syncBatch reads and loads the next batch, and reports whether it was full.
//
// The root span is created only once rows are found, back-dated to the start
// of the query, so idle polls of seven tables don't flood the traces.
func (t *tableSyncer) syncBatch(ctx context.Context) (bool, error) {
	start := time.Now()
	batch, err := t.deps.Sources.ChangedRows(ctx, t.ref, t.cursor, t.config.SafetyLag, t.config.BatchSize)
	queryEnd := time.Now()
	if err != nil {
		return false, err
	}
	if len(batch.Rows) == 0 {
		return false, nil
	}

	ctx, span := tracer.Start(ctx, "cdc.batch", trace.WithNewRoot(), trace.WithTimestamp(start),
		trace.WithAttributes(attribute.String("cdc.table", t.ref.String()), attribute.Int("cdc.rows", len(batch.Rows))))
	defer span.End()
	_, querySpan := tracer.Start(ctx, "cdc.pg_query", trace.WithTimestamp(start))
	querySpan.End(trace.WithTimestamp(queryEnd))

	outcome, next, err := t.load(ctx, batch)
	batchDuration.WithLabelValues(t.ref.String()).Observe(time.Since(start).Seconds())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "batch failed")
		return false, err
	}

	t.recordLoaded(ctx, outcome, next, len(batch.Rows), time.Since(start))
	return len(batch.Rows) == t.config.BatchSize, nil
}

// load sends batch to StarRocks and returns the cursor after it.
func (t *tableSyncer) load(ctx context.Context, batch Batch) (LoadOutcome, Cursor, error) {
	if t.hasNewColumns(batch.Columns) {
		if err := t.syncSchema(ctx); err != nil {
			return 0, Cursor{}, fmt.Errorf("adding new columns: %w", err)
		}
	}

	next, err := lastCursor(batch)
	if err != nil {
		return 0, Cursor{}, err
	}
	body, err := encodeRows(batch)
	if err != nil {
		return 0, Cursor{}, err
	}

	ctx, span := tracer.Start(ctx, "cdc.stream_load")
	defer span.End()

	outcome, err := t.deps.Loader.Load(ctx, t.ref.Table, loadLabel(t.ref, t.cursor, next), body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "stream load failed")
		return 0, Cursor{}, err
	}
	return outcome, next, nil
}

// hasNewColumns reports whether the source gained a column since the schema
// was last mirrored, for example after its service redeployed with a new
// field.
func (t *tableSyncer) hasNewColumns(columns []string) bool {
	for _, c := range columns {
		if !t.columns[c] {
			return true
		}
	}
	return false
}

func (t *tableSyncer) recordLoaded(ctx context.Context, outcome LoadOutcome, next Cursor, rows int, took time.Duration) {
	label := outcomeLoaded
	if outcome == AlreadyLoaded {
		label = outcomeAlreadyLoaded
	}
	batchesTotal.WithLabelValues(t.ref.String(), label).Inc()
	rowsSyncedTotal.WithLabelValues(t.ref.String()).Add(float64(rows))

	t.cursor = next
	t.deps.Tracker.recordLoad(t.ref, next, rows, time.Now())
	t.logger.InfoContext(ctx, "batch synced",
		"rows", rows, "outcome", label, "duration_ms", took.Milliseconds(),
		"cursor_updated_at", next.UpdatedAt, "cursor_id", next.ID)
}

// batchFailed keeps the cursor where it is, so the same rows are retried on
// the next poll. A source that has disappeared sends the table back through
// setup.
func (t *tableSyncer) batchFailed(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	batchesTotal.WithLabelValues(t.ref.String(), outcomeFailed).Inc()
	t.deps.Tracker.recordError(t.ref, err)

	if isMissingSource(err) {
		t.ready = false
		t.logger.WarnContext(ctx, "source disappeared, setting the table up again", "error", err)
		return
	}
	t.logger.ErrorContext(ctx, "batch failed, retrying on the next poll", "error", err)
}

// updateLag measures how far the cursor trails the newest source change.
func (t *tableSyncer) updateLag(ctx context.Context) {
	newest, err := t.deps.Sources.MaxUpdatedAt(ctx, t.ref)
	if err != nil {
		if ctx.Err() == nil {
			t.logger.DebugContext(ctx, "reading source max updated_at", "error", err)
		}
		return
	}

	lag := max(newest.Sub(t.cursor.UpdatedAt), 0)
	if newest.IsZero() {
		lag = 0
	}
	lagSeconds.WithLabelValues(t.ref.String()).Set(lag.Seconds())
	t.deps.Tracker.setLag(t.ref, lag)
}

func isMissingSource(err error) bool {
	if errors.Is(err, ErrSourceMissing) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == pgUndefinedTable || pgErr.Code == pgUnknownDatabase)
}
