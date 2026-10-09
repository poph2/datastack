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
SELECT LOWER(column_name) FROM information_schema.columns
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
// any source column the table lacks.
func (c *StarRocksCatalog) EnsureTable(ctx context.Context, table string, columns []Column) error {
	if err := c.exec(ctx, createDatabaseSQL(c.database)); err != nil {
		return err
	}

	existing, err := c.existingColumns(ctx, table)
	if err != nil {
		return err
	}
	for _, statement := range schemaStatements(c.database, table, columns, existing) {
		if err := c.exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
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

func (c *StarRocksCatalog) existingColumns(ctx context.Context, table string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, starRocksQueryTimeout)
	defer cancel()

	rows, err := c.db.QueryContext(ctx, existingColumnsQuery, c.database, table)
	if err != nil {
		return nil, fmt.Errorf("reading columns of starrocks table %s: %w", table, err)
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("reading columns of starrocks table %s: %w", table, err)
		}
		existing[strings.ToLower(name)] = true
	}
	return existing, rows.Err()
}

func (c *StarRocksCatalog) exec(ctx context.Context, statement string) error {
	ctx, cancel := context.WithTimeout(ctx, starRocksQueryTimeout)
	defer cancel()

	if _, err := c.db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("starrocks DDL %q: %w", truncate(statement), err)
	}
	return nil
}
