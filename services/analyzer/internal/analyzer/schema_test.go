package analyzer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTables() []Table {
	return []Table{
		{
			Name:    "customers",
			Comment: "Customers: one row per registered user.",
			Columns: []Column{
				{Name: "id", Type: "bigint(20)", Comment: "Surrogate primary key."},
				{Name: "loyalty_tier", Type: "varchar(80)", Comment: "BRONZE, SILVER or GOLD."},
			},
		},
		{
			Name:    "orders",
			Comment: "Checkout attempts: one row per checkout.",
			Columns: []Column{
				{Name: "id", Type: "BIGINT(20)", Comment: "Surrogate primary key."},
				{Name: "user_id", Type: "bigint(20)", Comment: "Customer who checked out. [ref: customers.id]"},
				{Name: "product_id", Type: "varchar(200)", Comment: "Product bought. [ref: inventory.product_id]"},
				{Name: "total_amount", Type: "decimal(10, 2)", Comment: "Order value in USD."},
			},
		},
	}
}

func TestParseJoins(t *testing.T) {
	tables := []Table{{
		Name: "attributions",
		Columns: []Column{
			{Name: "user_id", Comment: "Customer who clicked. [ref: customers.id]"},
			{Name: "utm_source", Comment: "Source. Join with utm_campaign. [ref: campaigns.utm_source]"},
			{Name: "note", Comment: "Mentions [ref: Bad.Case] and [ref: x] which are not markers."},
			{Name: "both", Comment: "[ref: a.b] then [ref: c_2.d_3]"},
		},
	}}

	assert.Equal(t, []Join{
		{From: "attributions.user_id", To: "customers.id"},
		{From: "attributions.utm_source", To: "campaigns.utm_source"},
		{From: "attributions.both", To: "a.b"},
		{From: "attributions.both", To: "c_2.d_3"},
	}, ParseJoins(tables))
}

func TestNewSchemaNormalisesTypesAndRendersPrompt(t *testing.T) {
	loadedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	schema := NewSchema("analytics_db", testTables(), loadedAt)

	assert.Equal(t, "bigint", schema.Tables[1].Columns[0].Type)
	assert.Equal(t, "varchar(200)", schema.Tables[1].Columns[2].Type)
	assert.Equal(t, "decimal(10, 2)", schema.Tables[1].Columns[3].Type)
	assert.Equal(t, loadedAt, schema.LoadedAt)
	require.Len(t, schema.Joins, 2)

	assert.Equal(t, `Database analytics_db (StarRocks, MySQL-compatible SQL).

Conventions for every table:
- Timestamps are DATETIME values in UTC. NOW() is UTC too.
- Money columns are DECIMAL amounts in USD with two decimal places.
- Rows are soft-deleted: add <alias>.deleted_at IS NULL for EVERY table in the query.
- updated_at is a technical change time. Never use it as a business event time: use created_at (or shipped_at, converted_at).

Columns every table has (not repeated below):
  id bigint: Surrogate primary key.

Tables:

TABLE customers: Customers: one row per registered user.
  loyalty_tier varchar(80): BRONZE, SILVER or GOLD.

TABLE orders: Checkout attempts: one row per checkout.
  user_id bigint: Customer who checked out. [ref: customers.id]
  product_id varchar(200): Product bought. [ref: inventory.product_id]
  total_amount decimal(10, 2): Order value in USD.

Joins (the only valid join conditions):
- orders.user_id = customers.id
- orders.product_id = inventory.product_id
`, schema.Prompt)
}

func TestSharedColumnsNeedTheSameTypeAndComment(t *testing.T) {
	tables := []Table{
		{Name: "a", Columns: []Column{{Name: "id", Type: "bigint", Comment: "Key."}, {Name: "status", Type: "varchar", Comment: "A status."}}},
		{Name: "b", Columns: []Column{{Name: "status", Type: "varchar", Comment: "B status."}, {Name: "id", Type: "bigint", Comment: "Key."}}},
	}
	assert.Equal(t, []Column{{Name: "id", Type: "bigint", Comment: "Key."}}, sharedColumns(tables))
	assert.Nil(t, sharedColumns(tables[:1]))
}

func TestSystemPromptHasRulesExamplesAndSchema(t *testing.T) {
	schema := NewSchema("analytics_db", testTables(), time.Now())
	prompt := SystemPrompt(schema)

	assert.Contains(t, prompt, "Reply with exactly one read-only SQL query")
	assert.Contains(t, prompt, "status IN ('PAID', 'SHIPPED')")
	assert.Contains(t, prompt, "Question: How many active customers are there in each country?")
	assert.Contains(t, prompt, schema.Prompt)
}

func TestRepairPrompt(t *testing.T) {
	prompt := repairPrompt("SELECT x FROM t", "Unknown column 'x'")
	assert.Contains(t, prompt, "SELECT x FROM t")
	assert.Contains(t, prompt, "Unknown column 'x'")

	assert.Contains(t, repairPrompt("", ErrNoSQL.Error()), "contained no SQL")
}
