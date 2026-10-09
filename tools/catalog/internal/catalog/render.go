package catalog

import (
	"fmt"
	"strings"
)

// header explains where the catalog comes from. It is part of the generated
// file, so the file is self-explanatory to someone who finds it cold.
const header = `# DataStack Data Catalog

> **Generated** by ` + "`make catalog`" + ` from the table and column comments in the services' databases. **Do not edit by hand:** change the ` + "`comment:`" + ` tag or ` + "`TableComment()`" + ` in the service's GORM model, then regenerate. ` + "`make catalog-check`" + ` fails when this file is stale.

The same comments are copied into StarRocks (` + "`analytics_db`" + `) by the CDC sync daemon, so a text-to-SQL analyzer reads exactly this documentation from ` + "`information_schema.tables.TABLE_COMMENT`" + ` and ` + "`information_schema.columns.COLUMN_COMMENT`" + `.

**Join markers.** A column that references another table ends its comment with ` + "`[ref: <table>.<column>]`" + `. These references are logical: the tables live in different services' databases, so nothing enforces them. In StarRocks every table sits in one ` + "`analytics_db`" + `, so the joins work directly. To build a join graph, parse the markers with ` + "`\\[ref: ([a-z_]+)\\.([a-z_]+)\\]`" + `.

**Conventions for every table.**
- Timestamps are UTC.
- Money is USD with two decimal places.
- Rows are soft-deleted: filter ` + "`deleted_at IS NULL`" + ` unless you are analysing deletions.
- ` + "`updated_at`" + ` is a technical change time, not a business event time.
`

// Render produces the catalog Markdown for tables (in the order given).
func Render(tables []Table) string {
	var b strings.Builder
	b.WriteString(header)

	b.WriteString("\n## Relationships\n\n")
	b.WriteString(renderDiagram(tables))
	b.WriteString("\n| From | To |\n|---|---|\n")
	for _, r := range Refs(tables) {
		fmt.Fprintf(&b, "| `%s.%s` | `%s.%s` |\n", r.FromTable, r.FromColumn, r.ToTable, r.ToColumn)
	}

	b.WriteString("\n## Tables\n\n| Table | Database | What one row is |\n|---|---|---|\n")
	for _, t := range tables {
		fmt.Fprintf(&b, "| [`%s`](#%s) | `%s` | %s |\n", t.Name, t.Name, t.Database, firstSentence(t.Comment))
	}

	for _, t := range tables {
		fmt.Fprintf(&b, "\n### %s\n\nDatabase `%s`. %s\n\n", t.Name, t.Database, t.Comment)
		b.WriteString("| Column | Type | Null | Description |\n|---|---|---|---|\n")
		for _, c := range t.Columns {
			null := ""
			if c.Nullable {
				null = "yes"
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", c.Name, c.Type, null, escapeCell(c.Comment))
		}
	}
	return b.String()
}

// renderDiagram draws the join graph as a Mermaid ER diagram, one edge per
// join marker.
func renderDiagram(tables []Table) string {
	var b strings.Builder
	b.WriteString("```mermaid\nerDiagram\n")
	for _, r := range Refs(tables) {
		fmt.Fprintf(&b, "  %s }o--|| %s : \"%s\"\n", r.FromTable, r.ToTable, r.FromColumn)
	}
	b.WriteString("```\n")
	return b.String()
}

// firstSentence returns the comment up to its first period, which by
// convention says what one row of the table is.
func firstSentence(comment string) string {
	if i := strings.Index(comment, ". "); i >= 0 {
		return escapeCell(comment[:i+1])
	}
	return escapeCell(comment)
}

func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
