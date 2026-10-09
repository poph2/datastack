package analyzer

import (
	"fmt"
	"slices"
	"strings"
)

// conventions hold for every table (data-catalog.md, "Conventions for every
// table"). They are global, so the comments don't repeat them per column.
var conventions = []string{
	"Timestamps are DATETIME values in UTC. NOW() is UTC too.",
	"Money columns are DECIMAL amounts in USD with two decimal places.",
	"Rows are soft-deleted: add <alias>.deleted_at IS NULL for EVERY table in the query.",
	"updated_at is a technical change time. Never use it as a business event time: use created_at (or shipped_at, converted_at).",
}

// rules turn the schema into a query. They encode the documented metric
// definitions that a small model otherwise gets wrong (revenue counts only
// PAID and SHIPPED orders, campaigns join on two columns, and so on).
var rules = []string{
	"Reply with exactly one read-only SQL query (SELECT, or WITH ... SELECT) and nothing else: no explanation, no markdown, no code fences, no trailing text.",
	"Use the StarRocks dialect, which is MySQL-compatible: DATE_TRUNC('day', ts), DATE(ts), TIMESTAMPDIFF(SECOND, start, end), NOW() - INTERVAL 1 DAY, ROUND(x, 2), COUNT(DISTINCT x).",
	"Use only the tables and columns listed below. Refer to tables by their bare names (the default database is already selected). Never invent a column.",
	"Join only along the listed joins. Join campaigns to attributions on BOTH utm_source AND utm_campaign. Join orders to inventory on orders.product_id = inventory.product_id (not inventory.id).",
	"Revenue means SUM(orders.total_amount) over orders with status IN ('PAID', 'SHIPPED'). Cleared revenue means SUM(payments.amount) over payments with status = 'SUCCESS'. Failed checkouts are orders with status = 'FAILED'.",
	"Enum values are upper-case strings exactly as listed in the comments, for example 'SHIPPED', 'GOLD', 'DECLINED'.",
	"Give every computed column a short snake_case alias. Round money and ratios with ROUND(x, 2). Compute ratios with a decimal division (x * 1.0 / NULLIF(y, 0)).",
	"For 'top N', 'highest' or 'lowest' questions use ORDER BY ... LIMIT N. Otherwise order the result in a natural way.",
	"Never select customers.email: it is personal data.",
}

// example is a worked question and answer. None of them overlaps the
// evaluation set, so the eval measures generalisation, not recall.
type example struct {
	question string
	sql      string
}

var examples = []example{
	{
		question: "How many active customers are there in each country?",
		sql: "SELECT c.country, COUNT(*) AS active_customers FROM customers c " +
			"WHERE c.deleted_at IS NULL AND c.status = 'ACTIVE' GROUP BY c.country ORDER BY active_customers DESC",
	},
	{
		question: "What was the daily revenue over the last 7 days?",
		sql: "SELECT DATE(o.created_at) AS day, ROUND(SUM(o.total_amount), 2) AS revenue FROM orders o " +
			"WHERE o.deleted_at IS NULL AND o.status IN ('PAID', 'SHIPPED') AND o.created_at >= NOW() - INTERVAL 7 DAY " +
			"GROUP BY DATE(o.created_at) ORDER BY day",
	},
	{
		question: "How many converted clicks did each utm_source bring?",
		sql: "SELECT a.utm_source, COUNT(*) AS conversions FROM attributions a " +
			"WHERE a.deleted_at IS NULL AND a.order_id IS NOT NULL GROUP BY a.utm_source ORDER BY conversions DESC",
	},
}

// RenderSchema renders the tables, their columns and the join list in a
// compact form: one line per column, comments verbatim, since they were
// written for exactly this reader.
func RenderSchema(schema *Schema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Database %s (StarRocks, MySQL-compatible SQL).\n\n", schema.Database)

	b.WriteString("Conventions for every table:\n")
	for _, convention := range conventions {
		fmt.Fprintf(&b, "- %s\n", convention)
	}

	shared := sharedColumns(schema.Tables)
	if len(shared) > 0 {
		b.WriteString("\nColumns every table has (not repeated below):\n")
		for _, column := range shared {
			fmt.Fprintf(&b, "  %s %s: %s\n", column.Name, column.Type, column.Comment)
		}
	}

	b.WriteString("\nTables:\n")
	for _, table := range schema.Tables {
		fmt.Fprintf(&b, "\nTABLE %s: %s\n", table.Name, table.Comment)
		for _, column := range table.Columns {
			if !slices.Contains(shared, column) {
				fmt.Fprintf(&b, "  %s %s: %s\n", column.Name, column.Type, column.Comment)
			}
		}
	}

	if len(schema.Joins) > 0 {
		b.WriteString("\nJoins (the only valid join conditions):\n")
		for _, join := range schema.Joins {
			fmt.Fprintf(&b, "- %s = %s\n", join.From, join.To)
		}
	}
	return b.String()
}

// sharedColumns returns the columns (name, type and comment) that every
// table has, in the order of the first table: the base model's id,
// created_at, updated_at and deleted_at. Listing them once saves about a
// fifth of the prompt, which matters to a CPU-bound local model.
func sharedColumns(tables []Table) []Column {
	if len(tables) < 2 {
		return nil
	}
	var shared []Column
	for _, column := range tables[0].Columns {
		inAll := true
		for _, table := range tables[1:] {
			if !slices.Contains(table.Columns, column) {
				inAll = false
				break
			}
		}
		if inAll {
			shared = append(shared, column)
		}
	}
	return shared
}

// SystemPrompt is the full instruction for the model: the rules, worked
// examples, then the schema.
func SystemPrompt(schema *Schema) string {
	var b strings.Builder
	b.WriteString("You translate business questions into a single SQL query over an e-commerce analytics database.\n\nRules:\n")
	for i, rule := range rules {
		fmt.Fprintf(&b, "%d. %s\n", i+1, rule)
	}

	b.WriteString("\nExamples:\n")
	for _, ex := range examples {
		fmt.Fprintf(&b, "\nQuestion: %s\nSQL: %s\n", ex.question, ex.sql)
	}

	b.WriteString("\n")
	b.WriteString(schema.Prompt)
	return b.String()
}

// questionPrompt is the user turn for a question.
func questionPrompt(question string) string {
	return "Question: " + question + "\nSQL:"
}

// repairPrompt is the user turn after a failed attempt: it shows the model
// its own query and why it failed, and asks for a corrected one.
func repairPrompt(sql, problem string) string {
	if sql == "" {
		return "Your reply contained no SQL query. " + problem +
			"\nReply with only the corrected SQL query, nothing else.\nSQL:"
	}
	return "This query failed:\n" + sql + "\n\nError: " + problem +
		"\n\nFix the query. Check every table and column against the schema. " +
		"Reply with only the corrected SQL query, nothing else.\nSQL:"
}
