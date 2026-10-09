// Package customer is the Customer Service domain: who the user is, where
// they are, their loyalty tier, and whether their account is in good standing.
package customer

import (
	"context"

	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
)

// LoyaltyTier segments customers for downstream revenue analytics.
type LoyaltyTier string

const (
	TierBronze LoyaltyTier = "BRONZE"
	TierSilver LoyaltyTier = "SILVER"
	TierGold   LoyaltyTier = "GOLD"
)

// Status is the account state. The Order Service rejects checkouts for any
// customer who is not ACTIVE.
type Status string

const (
	StatusActive          Status = "ACTIVE"
	StatusSuspended       Status = "SUSPENDED"
	StatusPendingDeletion Status = "PENDING_DELETION"
)

// Customer is a row in the customers table. The gorm tags are the schema:
// AutoMigrate creates the columns, CHECK constraints and indexes from them.
type Customer struct {
	database.Model
	// Unique among live customers only, so a deleted customer's email can
	// register again.
	Email string `gorm:"size:255;not null;uniqueIndex:,where:deleted_at IS NULL"`
	// ISO 3166-1 alpha-2.
	Country     string      `gorm:"size:2;not null"`
	LoyaltyTier LoyaltyTier `gorm:"size:20;not null;check:chk_customers_loyalty_tier,loyalty_tier IN ('BRONZE','SILVER','GOLD')"`
	Status      Status      `gorm:"size:20;not null;check:chk_customers_status,status IN ('ACTIVE','SUSPENDED','PENDING_DELETION')"`
}

// Migrate creates or updates the Customer Service schema.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return database.AutoMigrate(ctx, db, &Customer{})
}
