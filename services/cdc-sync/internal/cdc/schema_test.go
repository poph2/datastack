package cdc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStarRocksType(t *testing.T) {
	cases := []struct {
		column Column
		want   string
	}{
		{Column{DataType: "bigint"}, "BIGINT"},
		{Column{DataType: "integer"}, "INT"},
		{Column{DataType: "smallint"}, "SMALLINT"},
		{Column{DataType: "numeric", Precision: 10, Scale: 2}, "DECIMAL(10,2)"},
		{Column{DataType: "numeric", Precision: 60, Scale: 4}, "DECIMAL(38,4)"},
		{Column{DataType: "numeric"}, "DECIMAL(38,9)"},
		{Column{DataType: "character varying", MaxLength: 50}, "VARCHAR(200)"},
		{Column{DataType: "character varying", MaxLength: 100000}, "VARCHAR(65533)"},
		{Column{DataType: "character varying"}, "STRING"},
		{Column{DataType: "text"}, "STRING"},
		{Column{DataType: "boolean"}, "BOOLEAN"},
		{Column{DataType: "timestamp with time zone"}, "DATETIME"},
		{Column{DataType: "timestamp without time zone"}, "DATETIME"},
		{Column{DataType: "date"}, "DATE"},
		{Column{DataType: "double precision"}, "DOUBLE"},
		{Column{DataType: "real"}, "FLOAT"},
		{Column{DataType: "jsonb"}, "STRING"},
		{Column{DataType: "uuid"}, "STRING"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, starRocksType(tc.column), "%+v", tc.column)
	}
}

var ordersSchema = TableSchema{Columns: ordersColumns}

var ordersColumns = []Column{
	{Name: "created_at", DataType: "timestamp with time zone"},
	{Name: "id", DataType: "bigint"},
	{Name: "status", DataType: "character varying", MaxLength: 20},
	{Name: "total_amount", DataType: "numeric", Precision: 10, Scale: 2},
	{Name: "updated_at", DataType: "timestamp with time zone"},
}

func TestSchemaStatementsCreatesMissingTable(t *testing.T) {
	statements := structureStatements("analytics_db", "orders", ordersSchema, targetTable{})

	want := "CREATE TABLE IF NOT EXISTS `analytics_db`.`orders` (\n" +
		"  `id` BIGINT NOT NULL,\n" +
		"  `created_at` DATETIME NULL,\n" +
		"  `status` VARCHAR(80) NULL,\n" +
		"  `total_amount` DECIMAL(10,2) NULL,\n" +
		"  `updated_at` DATETIME NULL\n" +
		") PRIMARY KEY (`id`)\n" +
		"DISTRIBUTED BY HASH(`id`)\n" +
		"PROPERTIES (\"replication_num\" = \"1\")"
	assert.Equal(t, []string{want}, statements, "id leads even when Postgres orders it later")
}

func TestSchemaStatementsAddsOnlyMissingColumns(t *testing.T) {
	existing := targetTable{Columns: map[string]string{"id": "", "created_at": "", "updated_at": ""}}

	statements := structureStatements("analytics_db", "orders", ordersSchema, existing)

	assert.Equal(t, []string{
		"ALTER TABLE `analytics_db`.`orders` ADD COLUMN (`status` VARCHAR(80) NULL, `total_amount` DECIMAL(10,2) NULL)",
	}, statements)
}

func TestSchemaStatementsNothingToDo(t *testing.T) {
	existing := targetTable{Columns: map[string]string{
		"id": "", "created_at": "", "status": "", "total_amount": "", "updated_at": "", "dropped_in_pg": "",
	}}

	assert.Empty(t, structureStatements("analytics_db", "orders", ordersSchema, existing),
		"columns are only ever added, never dropped")
}

func TestSchemaStatementsCreatesTableWithComments(t *testing.T) {
	schema := TableSchema{
		Comment: `One row per order. Joins: customer_id [ref: customers.id]`,
		Columns: []Column{
			{Name: "id", DataType: "bigint", Comment: "Order id"},
			{Name: "customer_id", DataType: "bigint", Comment: `Buyer [ref: customers.id], "never" null`},
			{Name: "updated_at", DataType: "timestamp with time zone"},
		},
	}

	statements := structureStatements("analytics_db", "orders", schema, targetTable{})

	want := "CREATE TABLE IF NOT EXISTS `analytics_db`.`orders` (\n" +
		"  `id` BIGINT NOT NULL COMMENT 'Order id',\n" +
		"  `customer_id` BIGINT NULL COMMENT 'Buyer [ref: customers.id], \"never\" null',\n" +
		"  `updated_at` DATETIME NULL\n" +
		") PRIMARY KEY (`id`)\n" +
		"COMMENT 'One row per order. Joins: customer_id [ref: customers.id]'\n" +
		"DISTRIBUTED BY HASH(`id`)\n" +
		"PROPERTIES (\"replication_num\" = \"1\")"
	assert.Equal(t, []string{want}, statements, "uncommented columns get no COMMENT clause")
}

func TestSchemaStatementsAddsColumnsWithComments(t *testing.T) {
	schema := TableSchema{Columns: []Column{
		{Name: "id", DataType: "bigint"},
		{Name: "carrier", DataType: "text", Comment: "Shipping carrier"},
	}}
	existing := targetTable{Columns: map[string]string{"id": ""}}

	assert.Equal(t, []string{
		"ALTER TABLE `analytics_db`.`shipments` ADD COLUMN (`carrier` STRING NULL COMMENT 'Shipping carrier')",
	}, structureStatements("analytics_db", "shipments", schema, existing))
}

func TestValidateCursorColumns(t *testing.T) {
	assert.NoError(t, validateCursorColumns(ordersColumns))
	assert.Error(t, validateCursorColumns([]Column{{Name: "id", DataType: "bigint"}}))
}

func TestQuoteIdent(t *testing.T) {
	assert.Equal(t, "`we``ird`", quoteIdent("we`ird"))
}

func TestQuoteString(t *testing.T) {
	cases := map[string]string{
		"":                        "''",
		"plain":                   "'plain'",
		"it's":                    `'it\'s'`,
		`C:\path`:                 `'C:\\path'`,
		`\'`:                      `'\\\''`,
		`"quoted", [ref: a.b]; x`: `'"quoted", [ref: a.b]; x'`,
	}
	for in, want := range cases {
		assert.Equal(t, want, quoteString(in), "%q", in)
	}
}
