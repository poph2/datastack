// Package inventory is the Inventory Service domain: how much of each product
// is in stock, what it costs, and reserving or replenishing that stock.
package inventory

import (
	"github.com/shopspring/decimal"

	"github.com/poph2/datastack/pkgs/database"
)

// moneyDecimals is the number of decimal places prices are rendered with.
// Money is a shopspring decimal everywhere (never float64) and is serialised
// as a fixed-point string such as "49.99".
const moneyDecimals = 2

// Product is a row in the inventory table: one SKU's catalog data and its
// current stock.
type Product struct {
	database.Model
	ProductID   string
	Category    string
	StockLevel  int
	Price       decimal.Decimal `gorm:"type:decimal(10,2)"`
	AutoRestock bool
}

// TableName keeps the table name from the spec ("inventory") instead of
// GORM's default ("products").
func (Product) TableName() string {
	return "inventory"
}
