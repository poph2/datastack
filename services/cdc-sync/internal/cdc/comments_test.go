package cdc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

var commentedOrders = TableSchema{
	Comment: "One row per order",
	Columns: []Column{
		{Name: "id", DataType: "bigint", Comment: "Order id"},
		{Name: "customer_id", DataType: "bigint", Comment: "Buyer [ref: customers.id]"},
		{Name: "status", DataType: "character varying", MaxLength: 20, Comment: "PENDING, PAID or SHIPPED"},
		{Name: "total_amount", DataType: "numeric", Precision: 10, Scale: 2},
		{Name: "updated_at", DataType: "timestamp with time zone", Comment: "Set by trigger"},
	},
}

// inSync is a StarRocks table whose comments all match commentedOrders.
func inSync() targetTable {
	return targetTable{
		Comment: "One row per order",
		Columns: map[string]string{
			"id": "Order id", "customer_id": "Buyer [ref: customers.id]",
			"status": "PENDING, PAID or SHIPPED", "total_amount": "", "updated_at": "Set by trigger",
		},
	}
}

func TestCommentChangesNothingToDo(t *testing.T) {
	assert.Equal(t, CommentChanges{}, commentChanges("analytics_db", "orders", commentedOrders, inSync()))
}

func TestCommentChangesMissingTable(t *testing.T) {
	assert.Equal(t, CommentChanges{}, commentChanges("analytics_db", "orders", commentedOrders, targetTable{}),
		"CREATE TABLE carries the comments")
}

func TestCommentChangesTableComment(t *testing.T) {
	current := inSync()
	current.Comment = "old"

	assert.Equal(t, CommentChanges{Statements: []string{
		"ALTER TABLE `analytics_db`.`orders` COMMENT = 'One row per order'",
	}}, commentChanges("analytics_db", "orders", commentedOrders, current))
}

func TestCommentChangesBatchesColumnsIntoOneAlter(t *testing.T) {
	current := inSync()
	current.Comment = ""
	current.Columns["status"] = "old"
	current.Columns["total_amount"] = "removed in Postgres"
	current.Columns["customer_id"] = ""

	changes := commentChanges("analytics_db", "orders", commentedOrders, current)

	assert.Equal(t, []string{
		"ALTER TABLE `analytics_db`.`orders` COMMENT = 'One row per order'",
		"ALTER TABLE `analytics_db`.`orders` " +
			"MODIFY COLUMN `customer_id` BIGINT NULL COMMENT 'Buyer [ref: customers.id]', " +
			"MODIFY COLUMN `status` VARCHAR(80) NULL COMMENT 'PENDING, PAID or SHIPPED', " +
			"MODIFY COLUMN `total_amount` DECIMAL(10,2) NULL COMMENT ''",
	}, changes.Statements, "table comment first, then every column in one ALTER, with its type unchanged")
	assert.False(t, changes.KeyCommentDiffers)
}

func TestCommentChangesLeavesKeyColumnAlone(t *testing.T) {
	current := inSync()
	current.Columns["id"] = "stale"

	changes := commentChanges("analytics_db", "orders", commentedOrders, current)

	assert.Empty(t, changes.Statements)
	assert.True(t, changes.KeyCommentDiffers)
}

func TestCommentChangesSkipsColumnsNotYetAdded(t *testing.T) {
	current := inSync()
	delete(current.Columns, "status")

	assert.Empty(t, commentChanges("analytics_db", "orders", commentedOrders, current).Statements,
		"ADD COLUMN gives a new column its comment")
}

func TestCommentChangesMatchesColumnNamesCaseInsensitively(t *testing.T) {
	schema := TableSchema{Columns: []Column{{Name: "Status", DataType: "text", Comment: "c"}}}
	current := targetTable{Columns: map[string]string{"status": "c"}}

	assert.Empty(t, commentChanges("analytics_db", "orders", schema, current).Statements)
}

func TestCommentChangesEscapes(t *testing.T) {
	schema := TableSchema{Comment: `it's a \ backslash`, Columns: []Column{{Name: "id", DataType: "bigint"}}}
	current := targetTable{Columns: map[string]string{"id": ""}}

	assert.Equal(t, []string{"ALTER TABLE `analytics_db`.`t` COMMENT = 'it\\'s a \\\\ backslash'"},
		commentChanges("analytics_db", "t", schema, current).Statements)
}

func TestIsSchemaChangeInProgress(t *testing.T) {
	refused := fmt.Errorf("starrocks DDL: %w",
		errors.New("Error 1064 (HY000): A schema change operation is in progress on the table orders."))

	assert.True(t, isSchemaChangeInProgress(refused))
	assert.False(t, isSchemaChangeInProgress(errors.New("connection refused")))
	assert.False(t, isSchemaChangeInProgress(nil))
}
