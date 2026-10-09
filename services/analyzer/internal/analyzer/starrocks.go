package analyzer

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	starRocksDialTimeout  = 5 * time.Second
	starRocksAdminTimeout = 30 * time.Second
	starRocksPingTimeout  = 3 * time.Second
	starRocksMaxConns     = 4
	// readOnlyMaxConnections caps the read-only user's sessions on the FE,
	// so a burst of questions can't exhaust StarRocks connections.
	readOnlyMaxConnections = 16
	// socketTimeoutMargin keeps the driver's socket timeout above the
	// StarRocks query_timeout, so StarRocks reports the timeout itself.
	socketTimeoutMargin = 5 * time.Second
)

// identifier is what this package accepts as a user or database name in
// DDL, where neither can be passed as a parameter.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

const tablesQuery = `
SELECT table_name, COALESCE(table_comment, '') FROM information_schema.tables
 WHERE table_schema = ? ORDER BY table_name`

const columnsQuery = `
SELECT table_name, column_name, column_type, COALESCE(column_comment, '') FROM information_schema.columns
 WHERE table_schema = ? ORDER BY table_name, ordinal_position`

// StarRocksConfig locates StarRocks and names the two users: the admin that
// reads the schema and manages the read-only user, and the read-only user
// that runs every generated query.
type StarRocksConfig struct {
	Addr             string
	Database         string
	AdminUser        string
	AdminPassword    string
	ReadOnlyUser     string
	ReadOnlyPassword string
	QueryTimeout     time.Duration
}

// StarRocks holds the two connection pools.
type StarRocks struct {
	config   StarRocksConfig
	admin    *sql.DB
	readOnly *sql.DB
}

// OpenStarRocks prepares both pools without connecting, so the service can
// start while StarRocks is still coming up.
func OpenStarRocks(config StarRocksConfig) (*StarRocks, error) {
	for _, name := range []string{config.Database, config.ReadOnlyUser} {
		if !identifier.MatchString(name) {
			return nil, fmt.Errorf("invalid StarRocks identifier %q", name)
		}
	}

	adminSession := map[string]string{
		"new_planner_optimize_timeout": strconv.FormatInt(starRocksAdminTimeout.Milliseconds(), 10),
	}
	admin, err := openPool(config.Addr, config.AdminUser, config.AdminPassword, "", adminSession, starRocksAdminTimeout)
	if err != nil {
		return nil, err
	}
	// Session variables are set on every new connection of the read-only
	// pool: StarRocks enforces query_timeout server-side, and UTC makes
	// NOW() agree with the stored timestamps. The planner gets the whole
	// query timeout too (both pools): its 3s default fails even trivial
	// queries when the FE is short of CPU, which on a laptop looks like bad
	// SQL.
	session := map[string]string{
		"query_timeout":                strconv.Itoa(max(1, int(config.QueryTimeout.Seconds()))),
		"new_planner_optimize_timeout": strconv.FormatInt(config.QueryTimeout.Milliseconds(), 10),
		"time_zone":                    "'+00:00'",
	}
	readOnly, err := openPool(config.Addr, config.ReadOnlyUser, config.ReadOnlyPassword, config.Database, session,
		config.QueryTimeout+socketTimeoutMargin)
	if err != nil {
		return nil, err
	}
	return &StarRocks{config: config, admin: admin, readOnly: readOnly}, nil
}

func openPool(addr, user, password, database string, session map[string]string, timeout time.Duration) (*sql.DB, error) {
	dsn := mysql.NewConfig()
	dsn.Net = "tcp"
	dsn.Addr = addr
	dsn.User = user
	dsn.Passwd = password
	dsn.DBName = database
	dsn.Params = session
	dsn.Timeout = starRocksDialTimeout
	dsn.ReadTimeout = timeout
	dsn.WriteTimeout = timeout
	// Values are read as text and encoded by column type (encode.go), so
	// DATETIME stays the string StarRocks returns.
	dsn.ParseTime = false
	// Without parameters a query uses the text protocol, which StarRocks
	// supports better than server-side prepared statements.
	dsn.InterpolateParams = true

	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("opening starrocks connection pool for %s: %w", user, err)
	}
	db.SetMaxOpenConns(starRocksMaxConns)
	return db, nil
}

// Close releases both pools.
func (s *StarRocks) Close() error {
	return firstError(s.readOnly.Close(), s.admin.Close())
}

// Ping checks the FE answers.
func (s *StarRocks) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, starRocksPingTimeout)
	defer cancel()

	if err := s.admin.PingContext(ctx); err != nil {
		return fmt.Errorf("pinging starrocks: %w", err)
	}
	return nil
}

// EnsureReadOnlyUser creates the read-only user if needed, sets its
// password and grants it SELECT on every table of the analytics database.
//
// In StarRocks 3.3 a grant ON ALL TABLES IN DATABASE also covers tables
// created later, but it is bound to the database's internal id: if
// analytics_db is dropped and recreated, the grant silently disappears, and
// granting on a database that doesn't exist yet fails. Every statement is
// idempotent, so the schema refresh simply runs this again each time.
func (s *StarRocks) EnsureReadOnlyUser(ctx context.Context) error {
	user := fmt.Sprintf("'%s'@'%%'", s.config.ReadOnlyUser)
	password := quoteString(s.config.ReadOnlyPassword)
	statements := []string{
		fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s", user, password),
		fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s", user, password),
		fmt.Sprintf("SET PROPERTY FOR '%s' 'max_user_connections' = '%d'", s.config.ReadOnlyUser, readOnlyMaxConnections),
		fmt.Sprintf("GRANT SELECT ON ALL TABLES IN DATABASE %s TO USER %s", s.config.Database, user),
	}

	ctx, cancel := context.WithTimeout(ctx, starRocksAdminTimeout)
	defer cancel()
	for _, statement := range statements {
		if _, err := s.admin.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("preparing read-only user %s: %w", s.config.ReadOnlyUser, err)
		}
	}
	return nil
}

// LoadSchema reads every table and column of the analytics database with
// its comment.
func (s *StarRocks) LoadSchema(ctx context.Context) (*Schema, error) {
	ctx, cancel := context.WithTimeout(ctx, starRocksAdminTimeout)
	defer cancel()

	tables, err := s.readTables(ctx)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("database %s has no tables yet", s.config.Database)
	}
	if err := s.readColumns(ctx, tables); err != nil {
		return nil, err
	}

	ordered := make([]Table, 0, len(tables))
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		ordered = append(ordered, *tables[name])
	}
	return NewSchema(s.config.Database, ordered, time.Now().UTC()), nil
}

func (s *StarRocks) readTables(ctx context.Context) (map[string]*Table, error) {
	rows, err := s.admin.QueryContext(ctx, tablesQuery, s.config.Database)
	if err != nil {
		return nil, fmt.Errorf("reading tables of %s: %w", s.config.Database, err)
	}
	defer rows.Close()

	tables := map[string]*Table{}
	for rows.Next() {
		var table Table
		if err := rows.Scan(&table.Name, &table.Comment); err != nil {
			return nil, fmt.Errorf("reading tables of %s: %w", s.config.Database, err)
		}
		tables[table.Name] = &table
	}
	return tables, rows.Err()
}

func (s *StarRocks) readColumns(ctx context.Context, tables map[string]*Table) error {
	rows, err := s.admin.QueryContext(ctx, columnsQuery, s.config.Database)
	if err != nil {
		return fmt.Errorf("reading columns of %s: %w", s.config.Database, err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName string
		var column Column
		if err := rows.Scan(&tableName, &column.Name, &column.Type, &column.Comment); err != nil {
			return fmt.Errorf("reading columns of %s: %w", s.config.Database, err)
		}
		if table, ok := tables[tableName]; ok {
			table.Columns = append(table.Columns, column)
		}
	}
	return rows.Err()
}

// Explain asks StarRocks to plan sql as the read-only user, which catches
// syntax errors, unknown columns and missing privileges without running it.
func (s *StarRocks) Explain(ctx context.Context, query string) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	defer cancel()

	rows, err := s.readOnly.QueryContext(ctx, "EXPLAIN "+query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

// Query runs sql as the read-only user and returns at most maxRows rows.
// When more exist, Truncated is set and the rest is never transferred.
func (s *StarRocks) Query(ctx context.Context, query string, maxRows int) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	defer cancel()

	rows, err := s.readOnly.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := readResult(rows, maxRows)
	if err != nil {
		return nil, err
	}
	if result.Truncated {
		// Closing the rows would otherwise read the remaining result set to
		// the end. Cancelling drops the connection instead.
		cancel()
		return result, nil
	}
	return result, rows.Err()
}

// readResult scans up to maxRows rows and checks for one more.
func readResult(rows *sql.Rows, maxRows int) (*Result, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("reading result columns: %w", err)
	}
	columns := make([]ResultColumn, len(types))
	for i, t := range types {
		columns[i] = ResultColumn{Name: t.Name(), Type: strings.ToLower(t.DatabaseTypeName())}
	}

	result := &Result{Columns: columns, Rows: [][]any{}}
	raw := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range raw {
		targets[i] = &raw[i]
	}
	for rows.Next() {
		if len(result.Rows) == maxRows {
			result.Truncated = true
			break
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("reading result row: %w", err)
		}
		result.Rows = append(result.Rows, EncodeRow(columns, raw))
	}
	return result, nil
}

// quoteString renders s as a single-quoted SQL string literal.
func quoteString(s string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
	return "'" + escaped + "'"
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
