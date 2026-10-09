package cdc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	starRocksDialTimeout = 5 * time.Second
	// starRocksQueryTimeout covers DDL, which StarRocks acknowledges quickly
	// (schema changes run as background jobs).
	starRocksQueryTimeout = 30 * time.Second
	starRocksPingTimeout  = 3 * time.Second
	starRocksMaxConns     = 4
)

const existingColumnsQuery = `
SELECT LOWER(column_name), COALESCE(column_comment, '') FROM information_schema.columns
 WHERE table_schema = ? AND table_name = ?`

const tableCommentQuery = `
SELECT COALESCE(table_comment, '') FROM information_schema.tables
 WHERE table_schema = ? AND table_name = ?`

// StarRocksConfig locates the FE's MySQL-protocol port, used for DDL and for
// reading each table's starting cursor.
type StarRocksConfig struct {
	Addr     string
	Database string
	User     string
	Password string
}

// StarRocksCatalog manages the target tables over StarRocks' MySQL protocol.
type StarRocksCatalog struct {
	db       *sql.DB
	database string
}

// OpenStarRocks prepares a connection pool. It doesn't connect: StarRocks
// may still be starting, and the daemon must come up anyway and retry.
func OpenStarRocks(config StarRocksConfig) (*StarRocksCatalog, error) {
	dsn := mysql.NewConfig()
	dsn.Net = "tcp"
	dsn.Addr = config.Addr
	dsn.User = config.User
	dsn.Passwd = config.Password
	dsn.Timeout = starRocksDialTimeout
	dsn.ReadTimeout = starRocksQueryTimeout
	dsn.WriteTimeout = starRocksQueryTimeout
	// DATETIME values are UTC (the encoder writes them that way), so read
	// them back as UTC.
	dsn.ParseTime = true
	dsn.Loc = time.UTC
	// Client-side parameter interpolation avoids server-side prepared
	// statements, which the MySQL protocol front of StarRocks handles less
	// completely than MySQL does.
	dsn.InterpolateParams = true

	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("opening starrocks connection pool: %w", err)
	}
	db.SetMaxOpenConns(starRocksMaxConns)
	return &StarRocksCatalog{db: db, database: config.Database}, nil
}

// Close releases the connection pool.
func (c *StarRocksCatalog) Close() error {
	return c.db.Close()
}

// Ping checks that the FE answers queries. Readiness probes use it.
func (c *StarRocksCatalog) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, starRocksPingTimeout)
	defer cancel()

	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("pinging starrocks: %w", err)
	}
	return nil
}

// EnsureTable creates the analytics database and table if needed, and adds
// any source column the table lacks. A new table gets every comment; the
// comments of an existing one are left to SyncComments.
func (c *StarRocksCatalog) EnsureTable(ctx context.Context, table string, schema TableSchema) error {
	if err := c.exec(ctx, createDatabaseSQL(c.database)); err != nil {
		return err
	}

	current, err := c.currentTable(ctx, table)
	if err != nil {
		return err
	}
	for _, statement := range structureStatements(c.database, table, schema, current) {
		if err := c.exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// SyncComments brings the table and column comments of an existing table in
// line with schema. It returns the changes it attempted; on error some of
// them may not have run, and the next call recomputes what is left.
func (c *StarRocksCatalog) SyncComments(ctx context.Context, table string, schema TableSchema) (CommentChanges, error) {
	current, err := c.currentTable(ctx, table)
	if err != nil {
		return CommentChanges{}, err
	}

	changes := commentChanges(c.database, table, schema, current)
	for _, statement := range changes.Statements {
		if err := c.exec(ctx, statement); err != nil {
			return changes, err
		}
	}
	return changes, nil
}

// MaxUpdatedAt returns the newest updated_at already in table, or the zero
// time when the table is empty. It is where syncing resumes after a restart.
func (c *StarRocksCatalog) MaxUpdatedAt(ctx context.Context, table string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, starRocksQueryTimeout)
	defer cancel()

	query := fmt.Sprintf("SELECT MAX(%s) FROM %s.%s", quoteIdent(columnUpdatedAt), quoteIdent(c.database), quoteIdent(table))
	var newest sql.NullTime
	if err := c.db.QueryRowContext(ctx, query).Scan(&newest); err != nil {
		return time.Time{}, fmt.Errorf("reading max updated_at of starrocks table %s: %w", table, err)
	}
	return newest.Time, nil
}

// currentTable reads the target's columns and comments. A table that
// doesn't exist comes back with no columns.
func (c *StarRocksCatalog) currentTable(ctx context.Context, table string) (targetTable, error) {
	ctx, cancel := context.WithTimeout(ctx, starRocksQueryTimeout)
	defer cancel()

	columns, err := c.columnComments(ctx, table)
	if err != nil {
		return targetTable{}, err
	}
	current := targetTable{Columns: columns}
	if !current.exists() {
		return current, nil
	}

	if err := c.db.QueryRowContext(ctx, tableCommentQuery, c.database, table).Scan(&current.Comment); err != nil {
		return targetTable{}, fmt.Errorf("reading comment of starrocks table %s: %w", table, err)
	}
	return current, nil
}

// columnComments maps each lower-cased column name of table to its comment.
func (c *StarRocksCatalog) columnComments(ctx context.Context, table string) (map[string]string, error) {
	rows, err := c.db.QueryContext(ctx, existingColumnsQuery, c.database, table)
	if err != nil {
		return nil, fmt.Errorf("reading columns of starrocks table %s: %w", table, err)
	}
	defer rows.Close()

	columns := map[string]string{}
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, fmt.Errorf("reading columns of starrocks table %s: %w", table, err)
		}
		columns[strings.ToLower(name)] = comment
	}
	return columns, rows.Err()
}

func (c *StarRocksCatalog) exec(ctx context.Context, statement string) error {
	ctx, cancel := context.WithTimeout(ctx, starRocksQueryTimeout)
	defer cancel()

	if _, err := c.db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("starrocks DDL %q: %w", truncate(statement), err)
	}
	return nil
}
