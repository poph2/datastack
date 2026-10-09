package payment

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrNotFound means no live payment has the requested ID.
	ErrNotFound = errors.New("payment not found")
	// ErrAlreadyPaid means the order already has a successful payment.
	ErrAlreadyPaid = errors.New("order already paid")
	// ErrNotRefundable means the payment exists but is not SUCCESS.
	ErrNotRefundable = errors.New("payment not refundable")
)

// Repository reads and writes payments in the Payment Service database.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create inserts p and fills in its ID and timestamps. A second SUCCESS for
// the same order violates the partial unique index and returns
// ErrAlreadyPaid. The index, not a prior lookup, is what makes this hold when
// concurrent requests race.
func (r *Repository) Create(ctx context.Context, p *Payment) error {
	err := r.db.WithContext(ctx).Create(p).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrAlreadyPaid
	}
	if err != nil {
		return fmt.Errorf("creating payment for order %d: %w", p.OrderID, err)
	}
	return nil
}

// Get returns the live payment with the given ID.
func (r *Repository) Get(ctx context.Context, id uint64) (Payment, error) {
	var p Payment
	err := r.db.WithContext(ctx).First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Payment{}, ErrNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("getting payment %d: %w", id, err)
	}
	return p, nil
}

// Refund moves a SUCCESS payment to REFUNDED and returns it afterwards.
//
// The status check and the change are one conditional UPDATE, so two
// concurrent refunds of the same payment can't both succeed.
func (r *Repository) Refund(ctx context.Context, id uint64) (Payment, error) {
	var p Payment
	result := r.db.WithContext(ctx).
		Model(&p).
		Clauses(clause.Returning{}).
		Where("id = ? AND status = ?", id, StatusSuccess).
		Update("status", StatusRefunded)
	if result.Error != nil {
		return Payment{}, fmt.Errorf("refunding payment %d: %w", id, result.Error)
	}
	if result.RowsAffected == 1 {
		return p, nil
	}

	// Nothing matched: find out whether the payment is missing or not SUCCESS.
	if _, err := r.Get(ctx, id); err != nil {
		return Payment{}, err
	}
	return Payment{}, ErrNotRefundable
}
