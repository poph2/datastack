package catalog

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/pgtest"
)

func documentedTables() []Table {
	return []Table{
		{Database: "customers", Name: "customers", Comment: "Customers. One row per user.", Columns: []Column{
			{Name: "id", Type: "bigint", Comment: "Key."},
		}},
		{Database: "orders", Name: "orders", Comment: "Orders: one row per checkout. More.", Columns: []Column{
			{Name: "id", Type: "bigint", Comment: "Key."},
			{Name: "user_id", Type: "bigint", Comment: "Buyer. [ref: customers.id]"},
			{Name: "note", Type: "text", Nullable: true, Comment: "Free text | with a pipe."},
		}},
	}
}

func TestRefs(t *testing.T) {
	assert.Equal(t, []Ref{{FromTable: "orders", FromColumn: "user_id", ToTable: "customers", ToColumn: "id"}},
		Refs(documentedTables()))
}

func TestValidateAcceptsADocumentedSchema(t *testing.T) {
	assert.Empty(t, Validate(documentedTables()))
}

func TestValidateReportsProblems(t *testing.T) {
	tables := documentedTables()
	tables[0].Comment = ""
	tables[1].Columns[0].Comment = " "
	tables[1].Columns[1].Comment = "Buyer. [ref: customer.id]"
	tables[1].Columns[2].Comment = "See [ref customers.id]"
	tables = append(tables, Table{Database: "other", Name: "orders", Comment: "Dup."})

	assert.Equal(t, []string{
		"customers.customers: table has no comment",
		"orders.note: malformed join marker (want [ref: table.column])",
		"orders.orders.id: column has no comment",
		"orders.user_id: [ref: customer.id] points at a column that doesn't exist",
		"table orders exists in both orders and other",
	}, Validate(tables))
}

func TestRender(t *testing.T) {
	out := Render(documentedTables())

	assert.Contains(t, out, "Do not edit by hand")
	assert.Contains(t, out, `orders }o--|| customers : "user_id"`)
	assert.Contains(t, out, "| `orders.user_id` | `customers.id` |")
	assert.Contains(t, out, "| [`orders`](#orders) | `orders` | Orders: one row per checkout. |")
	assert.Contains(t, out, "| `note` | `text` | yes | Free text \\| with a pipe. |")
}

func TestReadFromPostgres(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgtest.Start(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	_, err = conn.Exec(ctx, `
		CREATE TABLE things (id BIGINT PRIMARY KEY, price DECIMAL(10,2), name VARCHAR(20) NOT NULL);
		COMMENT ON TABLE things IS 'One row per thing.';
		COMMENT ON COLUMN things.price IS 'Price in USD.';`)
	require.NoError(t, err)

	tables, err := Read(ctx, conn, "test")

	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "One row per thing.", tables[0].Comment)
	assert.Equal(t, []Column{
		{Name: "id", Type: "bigint", Nullable: false, Comment: ""},
		{Name: "price", Type: "numeric(10,2)", Nullable: true, Comment: "Price in USD."},
		{Name: "name", Type: "character varying(20)", Nullable: false, Comment: ""},
	}, tables[0].Columns)
}
