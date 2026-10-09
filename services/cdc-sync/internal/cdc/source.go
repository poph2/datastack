package cdc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxConnsPerDatabase keeps the daemon light on the shared Postgres: one
// table loop and its lag query never need more.
const maxConnsPerDatabase = 2

// columnsQuery reflects a table's columns in their declared order.
const columnsQuery = `
SELECT column_name, data_type,
       COALESCE(character_maximum_length, 0),
       COALESCE(numeric_precision, 0),
       COALESCE(numeric_scale, 0)
  FROM information_schema.columns
 WHERE table_schema = current_schema() AND table_name = $1
 ORDER BY ordinal_position`

// changedRowsQuery reads the next batch after the cursor. The row-value
// comparison on (updated_at, id) never skips rows that share the boundary
// timestamp, and is served by the idx_<table>_cdc_cursor index. Rows newer
// than the safety lag are left for a later poll, so a slow transaction that
// commits an older updated_at after this poll is still picked up. deleted_at
// is deliberately not filtered: soft deletes must reach StarRocks.
const changedRowsQuery = `
SELECT * FROM %s
 WHERE (updated_at, id) > ($1, $2)
   AND updated_at < NOW() - $3::bigint * INTERVAL '1 microsecond'
 ORDER BY updated_at, id
 LIMIT $4`

const maxUpdatedAtQuery = `SELECT MAX(updated_at) FROM %s`

// ErrSourceMissing means a configured source table doesn't exist (yet).
var ErrSourceMissing = errors.New("source table does not exist")

// Cursor is a position in a table's (updated_at, id) order.
type Cursor struct {
	UpdatedAt time.Time
	ID        int64
}

// Batch is one page of changed rows, decoded generically.
type Batch struct {
	Columns []string
	// Rows holds each row's values in Columns order, as pgx decodes them.
	Rows [][]any
	// TypeOIDs holds each column's Postgres type OID, so the encoder can tell
	// a date from a timestamp (pgx returns time.Time for both).
	TypeOIDs []uint32
}

// Sources reads the operational Postgres databases. It reads rows
// generically through pgx rather than GORM: the daemon has no model for any
// table, and needs each row's raw column values in whatever shape the table
// has today.
type Sources struct {
	base *pgxpool.Config

	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

// NewSources parses baseURL, a Postgres URL without a database. Each source
// database gets its own pool from it. Credentials may come from PGUSER and
// PGPASSWORD, which pgx reads natively.
func NewSources(baseURL string) (*Sources, error) {
	config, err := pgxpool.ParseConfig(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres URL: %w", err)
	}
	config.MaxConns = maxConnsPerDatabase
	// SELECT * changes shape whenever a service adds a column. pgx's default
	// statement cache would then fail with "cached plan must not change
	// result type", so describe each query afresh. One extra round trip per
	// poll is nothing at this rate.
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	return &Sources{base: config, pools: map[string]*pgxpool.Pool{}}, nil
}

// Close releases every pool.
func (s *Sources) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pool := range s.pools {
		pool.Close()
	}
}

// pool returns the pool for database, creating it on first use. Pools
// connect lazily, so a database that doesn't exist yet only fails the query.
func (s *Sources) pool(database string) (*pgxpool.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pool, ok := s.pools[database]; ok {
		return pool, nil
	}

	config := s.base.Copy()
	config.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("creating pool for database %s: %w", database, err)
	}
	s.pools[database] = pool
	return pool, nil
}

// Columns reflects ref's columns. It returns ErrSourceMissing (wrapped) when
// the table doesn't exist yet.
func (s *Sources) Columns(ctx context.Context, ref TableRef) ([]Column, error) {
	pool, err := s.pool(ref.Database)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, columnsQuery, ref.Table)
	if err != nil {
		return nil, fmt.Errorf("reading columns of %s: %w", ref, err)
	}
	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) {
		var c Column
		err := row.Scan(&c.Name, &c.DataType, &c.MaxLength, &c.Precision, &c.Scale)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("reading columns of %s: %w", ref, err)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("%s: %w", ref, ErrSourceMissing)
	}
	return columns, nil
}

// ChangedRows returns up to limit rows after cursor, excluding rows changed
// within safetyLag of the database clock.
func (s *Sources) ChangedRows(ctx context.Context, ref TableRef, after Cursor, safetyLag time.Duration, limit int) (Batch, error) {
	pool, err := s.pool(ref.Database)
	if err != nil {
		return Batch{}, err
	}

	query := fmt.Sprintf(changedRowsQuery, pgx.Identifier{ref.Table}.Sanitize())
	rows, err := pool.Query(ctx, query, after.UpdatedAt, after.ID, safetyLag.Microseconds(), limit)
	if err != nil {
		return Batch{}, fmt.Errorf("querying changed rows of %s: %w", ref, err)
	}
	defer rows.Close()

	batch := Batch{}
	for _, field := range rows.FieldDescriptions() {
		batch.Columns = append(batch.Columns, field.Name)
		batch.TypeOIDs = append(batch.TypeOIDs, field.DataTypeOID)
	}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return Batch{}, fmt.Errorf("decoding row of %s: %w", ref, err)
		}
		batch.Rows = append(batch.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return Batch{}, fmt.Errorf("reading changed rows of %s: %w", ref, err)
	}
	return batch, nil
}

// MaxUpdatedAt returns the newest updated_at in ref, or the zero time when
// the table is empty.
func (s *Sources) MaxUpdatedAt(ctx context.Context, ref TableRef) (time.Time, error) {
	pool, err := s.pool(ref.Database)
	if err != nil {
		return time.Time{}, err
	}

	var newest *time.Time
	query := fmt.Sprintf(maxUpdatedAtQuery, pgx.Identifier{ref.Table}.Sanitize())
	if err := pool.QueryRow(ctx, query).Scan(&newest); err != nil {
		return time.Time{}, fmt.Errorf("reading max updated_at of %s: %w", ref, err)
	}
	if newest == nil {
		return time.Time{}, nil
	}
	return *newest, nil
}
