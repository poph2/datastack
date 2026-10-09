package inventory

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// The deterministic demo catalog: PROD_0001 .. PROD_0100, the same in every
// environment, so k6 scripts and dashboards can rely on the IDs.
const (
	catalogSize = 100
	// Every lowStockEvery-th SKU starts with lowStockLevel units, so
	// out-of-stock failures appear within minutes of starting load.
	lowStockEvery = 20
	lowStockLevel = 3
	// Every neverRestockEvery-th SKU is never auto-restocked, so it stays
	// chronically scarce. PROD_0100 is both: it sells out quickly and stays out.
	neverRestockEvery = 25
)

// priceBand is a category's price range: base + spread*step, ending in .99.
type priceBand struct {
	category string
	base     int64
	step     int64
}

// Categories rotate through the catalog in this order.
var priceBands = []priceBand{
	{"ELECTRONICS", 99, 8},
	{"APPAREL", 19, 1},
	{"HOME", 29, 3},
	{"BEAUTY", 9, 1},
	{"SPORTS", 24, 2},
}

var priceCents = decimal.RequireFromString("0.99")

// seedCatalog inserts the demo catalog if the table has never held any
// products. Soft-deleted rows count, so a deliberately deleted catalog isn't
// recreated.
func seedCatalog(ctx context.Context, db *gorm.DB) error {
	var existing int64
	if err := db.WithContext(ctx).Unscoped().Model(&Product{}).Count(&existing).Error; err != nil {
		return fmt.Errorf("counting products: %w", err)
	}
	if existing > 0 {
		return nil
	}

	products := make([]Product, 0, catalogSize)
	for i := 1; i <= catalogSize; i++ {
		products = append(products, catalogProduct(i))
	}
	if err := db.WithContext(ctx).Create(&products).Error; err != nil {
		return fmt.Errorf("seeding catalog: %w", err)
	}
	return nil
}

// catalogProduct returns the i-th (1-based) catalog entry.
func catalogProduct(i int) Product {
	band := priceBands[(i-1)%len(priceBands)]
	// A spread in [0, 100) that looks random but is reproducible.
	spread := int64(i*7919) % 100

	dollars := band.base + spread*band.step

	stock := 40 + (i*37)%160
	if i%lowStockEvery == 0 {
		stock = lowStockLevel
	}

	return Product{
		ProductID:   fmt.Sprintf("PROD_%04d", i),
		Category:    band.category,
		StockLevel:  stock,
		Price:       decimal.NewFromInt(dollars).Add(priceCents),
		AutoRestock: i%neverRestockEvery != 0,
	}
}
