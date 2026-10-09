package database

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TableCommenter is implemented by models that document their table: what one
// row represents, which service owns it, and how it is typically queried.
//
// Column documentation lives in each field's `comment:` gorm tag. A column
// that references another table ends its comment with a join marker such as
// [ref: customers.id], naming the referenced table and column as they appear
// in StarRocks. Every comment reaches Postgres through AutoMigrate, and
// StarRocks through the CDC sync daemon, which is where a text-to-SQL analyzer
// reads them.
type TableCommenter interface {
	TableComment() string
}

// applyTableComment sets the table comment of model if it implements
// TableCommenter.
func applyTableComment(db *gorm.DB, model any, table string) error {
	commenter, ok := model.(TableCommenter)
	if !ok {
		return nil
	}
	// COMMENT ON can't take a bind parameter, so the dialector quotes the
	// text as a literal (the same way GORM applies column comments).
	literal := db.Dialector.Explain("$1", commenter.TableComment())
	err := db.Exec("COMMENT ON TABLE ? IS ?", clause.Table{Name: table}, gorm.Expr(literal)).Error
	if err != nil {
		return fmt.Errorf("commenting table %s: %w", table, err)
	}
	return nil
}

// undocumentedQuery lists tables and columns in the public schema that have
// no comment. A table without a comment is reported with an empty column.
const undocumentedQuery = `
SELECT c.relname AS table_name, '' AS column_name
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind = 'r'
   AND COALESCE(obj_description(c.oid, 'pg_class'), '') = ''
UNION ALL
SELECT c.relname, a.attname
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind = 'r'
   AND a.attnum > 0 AND NOT a.attisdropped
   AND COALESCE(col_description(c.oid, a.attnum), '') = ''
 ORDER BY 1, 2`

// Undocumented returns every table ("orders") and column ("orders.status") in
// the database's public schema that has no comment. Each service's tests
// assert it is empty after Migrate, so an undocumented column can't ship.
func Undocumented(ctx context.Context, db *gorm.DB) ([]string, error) {
	var rows []struct {
		TableName  string
		ColumnName string
	}
	if err := db.WithContext(ctx).Raw(undocumentedQuery).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("listing undocumented tables and columns: %w", err)
	}

	missing := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ColumnName == "" {
			missing = append(missing, r.TableName)
			continue
		}
		missing = append(missing, r.TableName+"."+r.ColumnName)
	}
	return missing, nil
}
