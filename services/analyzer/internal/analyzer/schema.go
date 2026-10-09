// Package analyzer answers natural-language business questions about the
// StarRocks analytics_db: it renders the documented schema into a prompt,
// asks an LLM for one SELECT, guards it, and runs it as a read-only user.
package analyzer

import (
	"regexp"
	"strings"
	"time"
)

// refMarker is the machine-readable join marker that ends a column comment,
// for example "[ref: customers.id]" (docs/microservices/README.md §3.2).
var refMarker = regexp.MustCompile(`\[ref: ([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\]`)

// intDisplayWidth matches the display width StarRocks reports on integer
// types ("bigint(20)"). It means nothing to a query, so the prompt drops it.
var intDisplayWidth = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|bigint|largeint)\(\d+\)`)

// Schema is the documented shape of the analytics database at one moment.
type Schema struct {
	Database string
	Tables   []Table
	Joins    []Join
	LoadedAt time.Time
	// Prompt is the schema rendered for the LLM, built once per load.
	Prompt string
}

// Table is one analytics table and its comment.
type Table struct {
	Name    string   `json:"name"`
	Comment string   `json:"comment"`
	Columns []Column `json:"columns"`
}

// Column is one column, its SQL type and its comment.
type Column struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Comment string `json:"comment"`
}

// Join is a logical reference from one column to another, taken from a
// [ref: table.column] marker.
type Join struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// NewSchema assembles a schema from the tables read out of
// information_schema: it normalises column types, derives the join graph
// from the markers and renders the prompt.
func NewSchema(database string, tables []Table, loadedAt time.Time) *Schema {
	for i := range tables {
		for j := range tables[i].Columns {
			tables[i].Columns[j].Type = normaliseType(tables[i].Columns[j].Type)
		}
	}
	schema := &Schema{
		Database: database,
		Tables:   tables,
		Joins:    ParseJoins(tables),
		LoadedAt: loadedAt,
	}
	schema.Prompt = RenderSchema(schema)
	return schema
}

// ParseJoins returns one join per [ref: …] marker, in table and column order.
// A marker pointing at a table or column the schema lacks is still returned:
// the comment is the source of truth, and the model may still use it.
func ParseJoins(tables []Table) []Join {
	var joins []Join
	for _, table := range tables {
		for _, column := range table.Columns {
			for _, match := range refMarker.FindAllStringSubmatch(column.Comment, -1) {
				joins = append(joins, Join{
					From: table.Name + "." + column.Name,
					To:   match[1] + "." + match[2],
				})
			}
		}
	}
	return joins
}

func normaliseType(columnType string) string {
	return intDisplayWidth.ReplaceAllString(strings.ToLower(strings.TrimSpace(columnType)), "$1")
}
