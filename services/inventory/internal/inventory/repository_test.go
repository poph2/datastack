package inventory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/pkgs/pgtest"
	"github.com/poph2/datastack/services/inventory/migrations"
)

// newTestRepository starts Postgres, applies the real migrations (including
// the seed catalog), and returns a repository plus the raw DB for setup and
// assertions.
func newTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	url := pgtest.Start(t)

	require.NoError(t, migrate.Up(ctx, url, migrations.FS, discardLogger))

	db, err := database.Open(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	return NewRepository(db), db
}

// insertProduct adds a test product outside the seeded PROD_ range.
func insertProduct(t *testing.T, db *gorm.DB, productID string, stock int, autoRestock bool) Product {
	t.Helper()
	p := Product{
		ProductID:   productID,
		Category:    "TEST",
		StockLevel:  stock,
		Price:       decimal.RequireFromString("19.99"),
		AutoRestock: autoRestock,
	}
	require.NoError(t, db.Create(&p).Error)
	return p
}

func TestSeedCatalog(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx := context.Background()

	products, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, products, 100)
	assert.Equal(t, "PROD_0001", products[0].ProductID)
	assert.Equal(t, "PROD_0100", products[99].ProductID)

	scarce, err := repo.Get(ctx, "PROD_0100")
	require.NoError(t, err)
	assert.Equal(t, 3, scarce.StockLevel)
	assert.False(t, scarce.AutoRestock)

	for _, p := range products {
		assert.True(t, p.Price.IsPositive(), p.ProductID)
		assert.Equal(t, "99", p.Price.StringFixed(2)[len(p.Price.StringFixed(2))-2:], "%s price ends in .99", p.ProductID)
	}
}

func TestRepository(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("reserve decrements and returns the product", func(t *testing.T) {
		insertProduct(t, db, "T_RESERVE", 5, true)

		p, err := repo.Reserve(ctx, "T_RESERVE", 2)

		require.NoError(t, err)
		assert.Equal(t, 3, p.StockLevel)
		assert.Equal(t, "19.99", p.Price.StringFixed(2))
		assert.Equal(t, "TEST", p.Category)
	})

	t.Run("reserve tells out of stock from not found", func(t *testing.T) {
		insertProduct(t, db, "T_SHORT", 1, true)

		_, err := repo.Reserve(ctx, "T_SHORT", 2)
		assert.ErrorIs(t, err, ErrOutOfStock)

		_, err = repo.Reserve(ctx, "T_MISSING", 1)
		assert.ErrorIs(t, err, ErrNotFound)

		got, err := repo.Get(ctx, "T_SHORT")
		require.NoError(t, err)
		assert.Equal(t, 1, got.StockLevel, "a failed reserve must not change stock")
	})

	t.Run("concurrent reserves never oversell", func(t *testing.T) {
		const stock, buyers = 20, 50
		insertProduct(t, db, "T_RACE", stock, true)

		var reserved, outOfStock atomic.Int32
		var wg sync.WaitGroup
		for range buyers {
			wg.Go(func() {
				_, err := repo.Reserve(ctx, "T_RACE", 1)
				switch {
				case err == nil:
					reserved.Add(1)
				case assert.ErrorIs(t, err, ErrOutOfStock):
					outOfStock.Add(1)
				}
			})
		}
		wg.Wait()

		assert.EqualValues(t, stock, reserved.Load())
		assert.EqualValues(t, buyers-stock, outOfStock.Load())
		got, err := repo.Get(ctx, "T_RACE")
		require.NoError(t, err)
		assert.Zero(t, got.StockLevel)
	})

	t.Run("add stock returns the new level", func(t *testing.T) {
		insertProduct(t, db, "T_ADD", 1, true)

		p, err := repo.AddStock(ctx, "T_ADD", 9)
		require.NoError(t, err)
		assert.Equal(t, 10, p.StockLevel)

		_, err = repo.AddStock(ctx, "T_MISSING", 1)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("soft-deleted products can't be reserved", func(t *testing.T) {
		p := insertProduct(t, db, "T_DELETED", 10, true)
		require.NoError(t, db.Delete(&p).Error)

		_, err := repo.Reserve(ctx, "T_DELETED", 1)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("stock can't go negative even with raw SQL", func(t *testing.T) {
		insertProduct(t, db, "T_CHECK", 1, true)
		err := db.Exec("UPDATE inventory SET stock_level = -1 WHERE product_id = 'T_CHECK'").Error
		assert.Error(t, err)
	})
}

func TestRestockBelow(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	// Isolate from the seeded catalog: lift every seeded SKU above the
	// threshold first.
	require.NoError(t, db.Exec("UPDATE inventory SET stock_level = 1000").Error)
	insertProduct(t, db, "T_LOW", 2, true)
	insertProduct(t, db, "T_SCARCE", 2, false)
	insertProduct(t, db, "T_FINE", 20, true)

	restocked, err := repo.RestockBelow(ctx, 10, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 1, restocked)

	stockOf := func(id string) int {
		p, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return p.StockLevel
	}
	assert.Equal(t, 52, stockOf("T_LOW"))
	assert.Equal(t, 2, stockOf("T_SCARCE"), "auto_restock=false is never restocked")
	assert.Equal(t, 20, stockOf("T_FINE"))

	// A second run, as another replica would do, finds nothing below the threshold.
	restocked, err = repo.RestockBelow(ctx, 10, 50)
	require.NoError(t, err)
	assert.Zero(t, restocked)
}

func TestMigrationsRollBackCleanly(t *testing.T) {
	ctx := context.Background()
	url := pgtest.Start(t)

	require.NoError(t, migrate.Up(ctx, url, migrations.FS, discardLogger))
	for range 3 {
		require.NoError(t, migrate.Down(ctx, url, migrations.FS, discardLogger))
	}
	require.NoError(t, migrate.Up(ctx, url, migrations.FS, discardLogger))
}
