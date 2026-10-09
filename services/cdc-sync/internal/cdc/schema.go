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
	// Comment is the column's COMMENT ON COLUMN text, "" when it has none.
	Comment string
}

// TableSchema is a source table as reflected from Postgres: its columns in
// declared order and its COMMENT ON TABLE text ("" when it has none). The
// comments are carried into StarRocks so information_schema there documents
// each table, column and join for anyone generating SQL against it.
type TableSchema struct {
	Comment string
	Columns []Column
}

// targetTable is what StarRocks already holds for a table, as read from its
// information_schema.
type targetTable struct {
	Comment string
	// Columns maps each lower-cased column name to its comment. Empty means
	// the table doesn't exist yet.
	Columns map[string]string
}

func (t targetTable) exists() bool {
	return len(t.Columns) > 0
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

// structureStatements returns the StarRocks DDL that gives database.table
// every source column: a CREATE TABLE (with all comments) when the target
// doesn't exist yet, otherwise one ADD COLUMN for whatever it lacks. Like
// GORM's AutoMigrate it only ever adds, never drops or alters, columns.
// Comments on existing columns are commentChanges' job.
func structureStatements(database, table string, desired TableSchema, current targetTable) []string {
	if !current.exists() {
		return []string{createTableSQL(database, table, desired)}
	}

	var missing []Column
	for _, c := range desired.Columns {
		if _, ok := current.Columns[strings.ToLower(c.Name)]; !ok {
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
// The table COMMENT has to sit between the key clause and DISTRIBUTED BY.
func createTableSQL(database, table string, schema TableSchema) string {
	defs := []string{keyColumnDefinition(schema.Columns)}
	for _, c := range schema.Columns {
		if c.Name != columnID {
			defs = append(defs, columnDefinition(c))
		}
	}

	tableComment := ""
	if schema.Comment != "" {
		tableComment = "\nCOMMENT " + quoteString(schema.Comment)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (\n  %s\n) PRIMARY KEY (%s)%s\nDISTRIBUTED BY HASH(%s)\nPROPERTIES (\"replication_num\" = \"1\")",
		quoteIdent(database), quoteIdent(table),
		strings.Join(defs, ",\n  "),
		quoteIdent(columnID), tableComment, quoteIdent(columnID))
}

// keyColumnDefinition defines id, the only NOT NULL column, with the source
// id's comment if it has one.
func keyColumnDefinition(columns []Column) string {
	definition := quoteIdent(columnID) + " BIGINT NOT NULL"
	for _, c := range columns {
		if c.Name == columnID {
			return definition + commentClause(c.Comment)
		}
	}
	return definition
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
	return columnTypeDefinition(c) + commentClause(c.Comment)
}

func columnTypeDefinition(c Column) string {
	return quoteIdent(c.Name) + " " + starRocksType(c) + " NULL"
}

// commentClause is empty for an uncommented column, so DDL stays as short as
// it was before comments were carried over.
func commentClause(comment string) string {
	if comment == "" {
		return ""
	}
	return " COMMENT " + quoteString(comment)
}

// quoteIdent quotes a StarRocks (MySQL-dialect) identifier.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// quoteString quotes a StarRocks string literal. Backslash is StarRocks'
// escape character, so it is escaped before the quote is.
func quoteString(text string) string {
	escaped := strings.ReplaceAll(text, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return "'" + escaped + "'"
}
