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
	ProductID string `gorm:"size:50;not null;uniqueIndex:,where:deleted_at IS NULL;comment:Business key of the product, such as PROD_0001. Other tables reference products by this value, not by id."`
	Category  string `gorm:"size:50;not null;comment:Product category: ELECTRONICS, APPAREL, HOME, BEAUTY or SPORTS."`
	// The CHECK is a backstop. Reservations already refuse to go below zero,
	// but a bug must never be able to sell stock that doesn't exist.
	StockLevel int             `gorm:"not null;check:chk_inventory_stock_level,stock_level >= 0;comment:Units available to sell right now. A checkout reserves (subtracts) units, a declined payment releases them back, and restocks add units. Never negative."`
	Price      decimal.Decimal `gorm:"type:decimal(10,2);not null;check:chk_inventory_price,price > 0;comment:Current unit list price in USD. The price charged at checkout is copied to orders.unit_price."`
	// false keeps a SKU out of the background restock, so it stays scarce and
	// lost-revenue dashboards have something to show. Deliberately no
	// default: GORM skips zero-valued fields that have one on insert, so a
	// default of true would silently turn false into true.
	AutoRestock bool `gorm:"not null;comment:true if the background restock tops this product up when stock_level falls below the restock threshold. false marks deliberately scarce products that stay out of stock once sold out."`
}

// TableComment documents the table for the semantic layer (see
// database.TableCommenter).
func (Product) TableComment() string {
	return "Product catalog and current stock (Inventory Service): one row per product (SKU). Other tables reference products by product_id, not id: join orders.product_id to inventory.product_id. stock_level is the live level after every reservation, release and restock. price is the current list price, while orders.unit_price holds the price actually charged."
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
