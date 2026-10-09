package customer

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrNotFound means no live customer has the requested ID.
	ErrNotFound = errors.New("customer not found")
	// ErrEmailExists means a live customer already uses the email.
	ErrEmailExists = errors.New("email already registered")
)

// Changes lists the fields an update may set. Nil fields are left unchanged.
type Changes struct {
	LoyaltyTier *LoyaltyTier
	Status      *Status
}

// Repository reads and writes customers in the Customer Service database.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create inserts c and fills in its ID and timestamps.
func (r *Repository) Create(ctx context.Context, c *Customer) error {
	err := r.db.WithContext(ctx).Create(c).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrEmailExists
	}
	if err != nil {
		return fmt.Errorf("creating customer: %w", err)
	}
	return nil
}

// Get returns the live customer with the given ID.
func (r *Repository) Get(ctx context.Context, id uint64) (Customer, error) {
	var c Customer
	err := r.db.WithContext(ctx).First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Customer{}, ErrNotFound
	}
	if err != nil {
		return Customer{}, fmt.Errorf("getting customer %d: %w", id, err)
	}
	return c, nil
}

// Update applies changes to the live customer with the given ID and returns
// the updated row.
func (r *Repository) Update(ctx context.Context, id uint64, changes Changes) (Customer, error) {
	updates := map[string]any{}
	if changes.LoyaltyTier != nil {
		updates["loyalty_tier"] = *changes.LoyaltyTier
	}
	if changes.Status != nil {
		updates["status"] = *changes.Status
	}
	if len(updates) == 0 {
		return r.Get(ctx, id)
	}

	var c Customer
	result := r.db.WithContext(ctx).
		Model(&c).
		Clauses(clause.Returning{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return Customer{}, fmt.Errorf("updating customer %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return Customer{}, ErrNotFound
	}
	return c, nil
}

// Delete soft-deletes the customer by setting deleted_at.
func (r *Repository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&Customer{}, id)
	if result.Error != nil {
		return fmt.Errorf("deleting customer %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
