// Package catalog reads the documented schema of every DataStack database (the
// table and column comments that the services' GORM models put there),
// validates it, and renders it as a Markdown data catalog.
//
// The same comments reach StarRocks through the CDC sync daemon, so the
// catalog shows exactly what a text-to-SQL analyzer will see.
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Table is one documented table and the database (service) that owns it.
type Table struct {
	Database string
	Name     string
	Comment  string
	Columns  []Column
}

// Column is one documented column.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	Comment  string
}

// Ref is a join marker found in a column comment: "[ref: customers.id]" on
// orders.user_id becomes Ref{From: orders.user_id, To: customers.id}.
type Ref struct {
	FromTable, FromColumn string
	ToTable, ToColumn     string
}

// refPattern matches the join marker convention documented in
// pkgs/database.TableCommenter.
var refPattern = regexp.MustCompile(`\[ref: ([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\]`)

const tablesQuery = `
SELECT c.relname, COALESCE(obj_description(c.oid, 'pg_class'), '')
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind = 'r'
 ORDER BY c.relname`

const columnsQuery = `
SELECT a.attname,
       format_type(a.atttypid, a.atttypmod),
       NOT a.attnotnull,
       COALESCE(col_description(a.attrelid, a.attnum), '')
  FROM pg_attribute a
 WHERE a.attrelid = $1::regclass AND a.attnum > 0 AND NOT a.attisdropped
 ORDER BY a.attnum`

// Read returns every table in the public schema of the connected database.
func Read(ctx context.Context, conn *pgx.Conn, database string) ([]Table, error) {
	rows, err := conn.Query(ctx, tablesQuery)
	if err != nil {
		return nil, fmt.Errorf("listing tables in %s: %w", database, err)
	}
	tables, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Table, error) {
		t := Table{Database: database}
		return t, row.Scan(&t.Name, &t.Comment)
	})
	if err != nil {
		return nil, fmt.Errorf("reading tables in %s: %w", database, err)
	}

	for i := range tables {
		rows, err := conn.Query(ctx, columnsQuery, pgx.Identifier{tables[i].Name}.Sanitize())
		if err != nil {
			return nil, fmt.Errorf("listing columns of %s.%s: %w", database, tables[i].Name, err)
		}
		tables[i].Columns, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Column])
		if err != nil {
			return nil, fmt.Errorf("reading columns of %s.%s: %w", database, tables[i].Name, err)
		}
	}
	return tables, nil
}

// Refs extracts every join marker from the tables' column comments.
func Refs(tables []Table) []Ref {
	var refs []Ref
	for _, t := range tables {
		for _, c := range t.Columns {
			for _, m := range refPattern.FindAllStringSubmatch(c.Comment, -1) {
				refs = append(refs, Ref{FromTable: t.Name, FromColumn: c.Name, ToTable: m[1], ToColumn: m[2]})
			}
		}
	}
	return refs
}

// Validate reports every undocumented table or column, every malformed join
// marker, every marker pointing at a table or column that doesn't exist, and
// every table name used by more than one database (StarRocks merges them into
// one analytics_db, so names must be unique).
func Validate(tables []Table) []string {
	var problems []string
	columns := map[string]bool{}
	owner := map[string]string{}

	for _, t := range tables {
		if other, ok := owner[t.Name]; ok {
			problems = append(problems, fmt.Sprintf("table %s exists in both %s and %s", t.Name, other, t.Database))
		}
		owner[t.Name] = t.Database
		if strings.TrimSpace(t.Comment) == "" {
			problems = append(problems, fmt.Sprintf("%s.%s: table has no comment", t.Database, t.Name))
		}
		for _, c := range t.Columns {
			columns[t.Name+"."+c.Name] = true
			if strings.TrimSpace(c.Comment) == "" {
				problems = append(problems, fmt.Sprintf("%s.%s.%s: column has no comment", t.Database, t.Name, c.Name))
			}
			if strings.Contains(c.Comment, "[ref") && !refPattern.MatchString(c.Comment) {
				problems = append(problems, fmt.Sprintf("%s.%s: malformed join marker (want [ref: table.column])", t.Name, c.Name))
			}
		}
	}

	for _, r := range Refs(tables) {
		if !columns[r.ToTable+"."+r.ToColumn] {
			problems = append(problems, fmt.Sprintf("%s.%s: [ref: %s.%s] points at a column that doesn't exist",
				r.FromTable, r.FromColumn, r.ToTable, r.ToColumn))
		}
	}

	slices.Sort(problems)
	return problems
}
