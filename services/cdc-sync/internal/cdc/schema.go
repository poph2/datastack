package cdc

import (
	"fmt"
	"strings"
)

// Columns every source table must have. They come from database.Model: id is
// the StarRocks primary key and, with updated_at, the sync cursor.
const (
	columnID        = "id"
	columnUpdatedAt = "updated_at"
)

const (
	// maxVarcharBytes is StarRocks' VARCHAR limit used here. StarRocks counts
	// VARCHAR length in bytes, Postgres in characters.
	maxVarcharBytes = 65533
	// maxUTF8BytesPerChar sizes a VARCHAR(n) so any n UTF-8 characters fit.
	maxUTF8BytesPerChar = 4
	// maxDecimalPrecision is the widest DECIMAL StarRocks supports.
	maxDecimalPrecision = 38
	// Unconstrained Postgres numeric has no declared precision or scale.
	// DECIMAL(38,9) keeps it summable in dashboards and holds any amount of
	// money the demo will see.
	unconstrainedDecimalType = "DECIMAL(38,9)"
	// fallbackType is used for any Postgres type without a closer match. It
	// loses nothing: every value can be loaded as its text form.
	fallbackType = "STRING"
)

// Column is one source column as reflected from Postgres
// information_schema.columns.
type Column struct {
	Name string
	// DataType is information_schema's data_type, such as "bigint" or
	// "character varying".
	DataType string
	// MaxLength is character_maximum_length, 0 when the type has none.
	MaxLength int
	// Precision and Scale are numeric_precision and numeric_scale for
	// numeric columns, 0 when unconstrained.
	Precision int
	Scale     int
}

// starRocksType maps a Postgres column to the StarRocks type it is stored as.
func starRocksType(c Column) string {
	switch c.DataType {
	case "bigint":
		return "BIGINT"
	case "integer":
		return "INT"
	case "smallint":
		return "SMALLINT"
	case "numeric":
		return decimalType(c.Precision, c.Scale)
	case "character varying":
		return varcharType(c.MaxLength)
	case "text":
		return "STRING"
	case "boolean":
		return "BOOLEAN"
	case "timestamp with time zone", "timestamp without time zone":
		return "DATETIME"
	case "date":
		return "DATE"
	case "double precision":
		return "DOUBLE"
	case "real":
		return "FLOAT"
	default:
		return fallbackType
	}
}

func decimalType(precision, scale int) string {
	if precision == 0 {
		return unconstrainedDecimalType
	}
	return fmt.Sprintf("DECIMAL(%d,%d)", min(precision, maxDecimalPrecision), scale)
}

func varcharType(maxLength int) string {
	if maxLength == 0 {
		return "STRING"
	}
	return fmt.Sprintf("VARCHAR(%d)", min(maxLength*maxUTF8BytesPerChar, maxVarcharBytes))
}

// validateCursorColumns checks that a source table has the columns the sync
// cursor and the StarRocks primary key need.
func validateCursorColumns(columns []Column) error {
	var hasID, hasUpdatedAt bool
	for _, c := range columns {
		hasID = hasID || c.Name == columnID
		hasUpdatedAt = hasUpdatedAt || c.Name == columnUpdatedAt
	}
	if !hasID || !hasUpdatedAt {
		return fmt.Errorf("table needs %q and %q columns (embed database.Model)", columnID, columnUpdatedAt)
	}
	return nil
}

// schemaStatements returns the StarRocks DDL that makes database.table match
// the source columns. existing holds the (lower-cased) column names the
// target already has: empty means the table doesn't exist yet. Like GORM's
// AutoMigrate it only ever adds, never drops or alters, columns.
func schemaStatements(database, table string, source []Column, existing map[string]bool) []string {
	if len(existing) == 0 {
		return []string{createTableSQL(database, table, source)}
	}

	var missing []Column
	for _, c := range source {
		if !existing[strings.ToLower(c.Name)] {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{addColumnsSQL(database, table, missing)}
}

func createDatabaseSQL(database string) string {
	return "CREATE DATABASE IF NOT EXISTS " + quoteIdent(database)
}

// createTableSQL builds a Primary Key table keyed on id, so a re-sent row
// upserts in place. That is what makes at-least-once delivery safe.
// StarRocks requires key columns first, so id leads.
func createTableSQL(database, table string, columns []Column) string {
	defs := []string{quoteIdent(columnID) + " BIGINT NOT NULL"}
	for _, c := range columns {
		if c.Name != columnID {
			defs = append(defs, columnDefinition(c))
		}
	}

	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (\n  %s\n) PRIMARY KEY (%s)\nDISTRIBUTED BY HASH(%s)\nPROPERTIES (\"replication_num\" = \"1\")",
		quoteIdent(database), quoteIdent(table),
		strings.Join(defs, ",\n  "),
		quoteIdent(columnID), quoteIdent(columnID))
}

// addColumnsSQL adds every missing column in one statement. StarRocks runs
// schema changes as asynchronous jobs and rejects a second one while the
// first is running, so one ALTER per column would fail.
func addColumnsSQL(database, table string, columns []Column) string {
	defs := make([]string, 0, len(columns))
	for _, c := range columns {
		defs = append(defs, columnDefinition(c))
	}
	return fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN (%s)",
		quoteIdent(database), quoteIdent(table), strings.Join(defs, ", "))
}

// columnDefinition leaves every non-key column nullable: soft-delete columns
// are NULL for live rows, and nullability never blocks a load.
func columnDefinition(c Column) string {
	return quoteIdent(c.Name) + " " + starRocksType(c) + " NULL"
}

// quoteIdent quotes a StarRocks (MySQL-dialect) identifier.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
