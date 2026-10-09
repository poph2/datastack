// Package inventory is the Inventory Service domain: how much of each product
// is in stock, what it costs, and reserving or replenishing that stock.
package inventory

import (
	"context"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
)

// moneyDecimals is the number of decimal places prices are rendered with.
// Money is a shopspring decimal everywhere (never float64) and is serialised
// as a fixed-point string such as "49.99".
const moneyDecimals = 2

// Product is a row in the inventory table: one SKU's catalog data and its
// current stock. The gorm tags are the schema: AutoMigrate creates the
// columns, CHECK constraints and indexes from them.
type Product struct {
	database.Model
	// Unique among live products only (see the soft-delete convention).
	ProductID string `gorm:"size:50;not null;uniqueIndex:,where:deleted_at IS NULL"`
	Category  string `gorm:"size:50;not null"`
	// The CHECK is a backstop. Reservations already refuse to go below zero,
	// but a bug must never be able to sell stock that doesn't exist.
	StockLevel int             `gorm:"not null;check:chk_inventory_stock_level,stock_level >= 0"`
	Price      decimal.Decimal `gorm:"type:decimal(10,2);not null;check:chk_inventory_price,price > 0"`
	// false keeps a SKU out of the background restock, so it stays scarce and
	// lost-revenue dashboards have something to show. Deliberately no
	// default: GORM skips zero-valued fields that have one on insert, so a
	// default of true would silently turn false into true.
	AutoRestock bool `gorm:"not null"`
}

// TableName keeps the table name from the spec ("inventory") instead of
// GORM's default ("products").
func (Product) TableName() string {
	return "inventory"
}

// Migrate creates or updates the Inventory Service schema and loads the seed
// catalog into an empty table.
func Migrate(ctx context.Context, db *gorm.DB) error {
	if err := database.AutoMigrate(ctx, db, &Product{}); err != nil {
		return err
	}
	return seedCatalog(ctx, db)
}
