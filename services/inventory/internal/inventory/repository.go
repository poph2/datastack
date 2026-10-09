package inventory

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrNotFound means no live product has the requested product ID.
	ErrNotFound = errors.New("product not found")
	// ErrOutOfStock means the product exists but has fewer units than requested.
	ErrOutOfStock = errors.New("insufficient stock")
)

// Repository reads and writes stock in the Inventory Service database.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// List returns every live product, ordered by product ID. The catalog is a
// fixed demo set of about a hundred SKUs, so it isn't paginated.
func (r *Repository) List(ctx context.Context) ([]Product, error) {
	var products []Product
	if err := r.db.WithContext(ctx).Order("product_id").Find(&products).Error; err != nil {
		return nil, fmt.Errorf("listing products: %w", err)
	}
	return products, nil
}

// Get returns the live product with the given product ID.
func (r *Repository) Get(ctx context.Context, productID string) (Product, error) {
	var p Product
	err := r.db.WithContext(ctx).Where("product_id = ?", productID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, fmt.Errorf("getting product %s: %w", productID, err)
	}
	return p, nil
}

// Reserve takes quantity units out of stock and returns the product as it is
// afterwards.
//
// The check and the decrement are one conditional UPDATE. Postgres locks the
// row, and under READ COMMITTED a concurrent reserve re-evaluates the WHERE
// clause against the new stock level. So when two checkouts race for the last
// unit, exactly one of them succeeds, without explicit locks or transactions.
func (r *Repository) Reserve(ctx context.Context, productID string, quantity int) (Product, error) {
	var p Product
	result := r.db.WithContext(ctx).
		Model(&p).
		Clauses(clause.Returning{}).
		Where("product_id = ? AND stock_level >= ?", productID, quantity).
		Update("stock_level", gorm.Expr("stock_level - ?", quantity))
	if result.Error != nil {
		return Product{}, fmt.Errorf("reserving %d of %s: %w", quantity, productID, result.Error)
	}
	if result.RowsAffected == 1 {
		return p, nil
	}

	// Nothing matched: find out whether the product is missing or short.
	if _, err := r.Get(ctx, productID); err != nil {
		return Product{}, err
	}
	return Product{}, ErrOutOfStock
}

// AddStock puts quantity units back into stock: returned by a cancelled
// checkout, or delivered by a restock. It returns the product afterwards.
func (r *Repository) AddStock(ctx context.Context, productID string, quantity int) (Product, error) {
	var p Product
	result := r.db.WithContext(ctx).
		Model(&p).
		Clauses(clause.Returning{}).
		Where("product_id = ?", productID).
		Update("stock_level", gorm.Expr("stock_level + ?", quantity))
	if result.Error != nil {
		return Product{}, fmt.Errorf("adding %d to %s: %w", quantity, productID, result.Error)
	}
	if result.RowsAffected == 0 {
		return Product{}, ErrNotFound
	}
	return p, nil
}

// RestockBelow adds quantity units to every auto-restocked product whose
// stock is below threshold, and returns how many products it restocked.
//
// The WHERE clause is re-checked against each row's latest version, so if
// several replicas run this at the same moment, a product already lifted to
// the threshold or above is skipped. That is why the restocker requires
// quantity >= threshold.
func (r *Repository) RestockBelow(ctx context.Context, threshold, quantity int) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&Product{}).
		Where("auto_restock AND stock_level < ?", threshold).
		Update("stock_level", gorm.Expr("stock_level + ?", quantity))
	if result.Error != nil {
		return 0, fmt.Errorf("restocking products below %d: %w", threshold, result.Error)
	}
	return result.RowsAffected, nil
}
