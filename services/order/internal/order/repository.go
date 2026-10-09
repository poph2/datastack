package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrNotFound means no live order has the requested ID.
	ErrNotFound = errors.New("order not found")
	// ErrStatusChanged means the order was no longer in the status a
	// transition expected, for example because a reconciler moved it first.
	ErrStatusChanged = errors.New("order status changed concurrently")
)

// Transition moves an order from one status to another, optionally setting
// prices or a failure reason along the way. Nil fields are left unchanged.
type Transition struct {
	From, To      Status
	UnitPrice     *decimal.Decimal
	TotalAmount   *decimal.Decimal
	FailureReason *FailureReason
	// UnchangedSince, when set, also requires updated_at to still equal it:
	// the order hasn't been touched since it was read. The reconciler uses it
	// to claim a stranded order, since even a PENDING -> PENDING transition
	// moves updated_at.
	UnchangedSince *time.Time
}

// Repository reads and writes orders in the Order Service database.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create inserts o and fills in its ID and timestamps.
func (r *Repository) Create(ctx context.Context, o *Order) error {
	if err := r.db.WithContext(ctx).Create(o).Error; err != nil {
		return fmt.Errorf("creating order: %w", err)
	}
	return nil
}

// Get returns the live order with the given ID.
func (r *Repository) Get(ctx context.Context, id uint64) (Order, error) {
	var o Order
	err := r.db.WithContext(ctx).First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("getting order %d: %w", id, err)
	}
	return o, nil
}

// Apply performs t on order id and returns the updated order. It is one
// guarded UPDATE: it only matches while the order is still in t.From (and,
// with UnchangedSince, still untouched), so the checkout and the reconciler
// can never both move the same order. A lost guard is ErrStatusChanged.
func (r *Repository) Apply(ctx context.Context, id uint64, t Transition) (Order, error) {
	updates := map[string]any{"status": t.To}
	if t.UnitPrice != nil {
		updates["unit_price"] = *t.UnitPrice
	}
	if t.TotalAmount != nil {
		updates["total_amount"] = *t.TotalAmount
	}
	if t.FailureReason != nil {
		updates["failure_reason"] = *t.FailureReason
	}

	query := r.db.WithContext(ctx).Where("id = ? AND status = ?", id, t.From)
	if t.UnchangedSince != nil {
		query = query.Where("updated_at = ?", *t.UnchangedSince)
	}

	var o Order
	result := query.
		Model(&o).
		Clauses(clause.Returning{}).
		Updates(updates)
	if result.Error != nil {
		return Order{}, fmt.Errorf("moving order %d from %s to %s: %w", id, t.From, t.To, result.Error)
	}
	if result.RowsAffected == 1 {
		return o, nil
	}

	if _, err := r.Get(ctx, id); err != nil {
		return Order{}, err
	}
	return Order{}, ErrStatusChanged
}

// StuckPaid returns up to limit orders that have been PAID for longer than
// olderThan: paid, but the shipment request never went through.
func (r *Repository) StuckPaid(ctx context.Context, olderThan time.Duration, limit int) ([]Order, error) {
	return r.untouchedFor(ctx, StatusPaid, olderThan, limit)
}

// StuckPending returns up to limit orders that have been PENDING, untouched,
// for longer than olderThan: a checkout that died halfway.
func (r *Repository) StuckPending(ctx context.Context, olderThan time.Duration, limit int) ([]Order, error) {
	return r.untouchedFor(ctx, StatusPending, olderThan, limit)
}

// untouchedFor returns up to limit orders in status whose updated_at is older
// than olderThan, oldest first. The age is measured with the database clock,
// like every other timestamp.
func (r *Repository) untouchedFor(ctx context.Context, status Status, olderThan time.Duration, limit int) ([]Order, error) {
	var orders []Order
	err := r.db.WithContext(ctx).
		Where("status = ? AND updated_at < NOW() - make_interval(secs => ?)", status, olderThan.Seconds()).
		Order("updated_at").
		Limit(limit).
		Find(&orders).Error
	if err != nil {
		return nil, fmt.Errorf("finding orders stuck in %s: %w", status, err)
	}
	return orders, nil
}
