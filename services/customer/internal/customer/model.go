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
	Email string `gorm:"size:255;not null;uniqueIndex:,where:deleted_at IS NULL;comment:Login email, lower-cased, unique among live customers. Personal data: avoid returning it in analytical results."`
	// ISO 3166-1 alpha-2.
	Country     string      `gorm:"size:2;not null;comment:Country the customer lives in, as an ISO 3166-1 alpha-2 code such as US, GB or DE. Also the destination of their shipments."`
	LoyaltyTier LoyaltyTier `gorm:"size:20;not null;check:chk_customers_loyalty_tier,loyalty_tier IN ('BRONZE','SILVER','GOLD');comment:Current loyalty tier: BRONZE (most customers), SILVER or GOLD. Customers can be upgraded later, so this is the tier today, not at the time of a past order."`
	Status      Status      `gorm:"size:20;not null;check:chk_customers_status,status IN ('ACTIVE','SUSPENDED','PENDING_DELETION');comment:Account state: ACTIVE, SUSPENDED (checkouts are rejected with orders.failure_reason CUSTOMER_SUSPENDED) or PENDING_DELETION (the user asked for the account to be deleted)."`
}

// TableComment documents the table for the semantic layer (see
// database.TableCommenter).
func (Customer) TableComment() string {
	return "Customers (Customer Service): one row per registered user. The main who and where dimension: orders.user_id and attributions.user_id join to customers.id. created_at is the sign-up time, which makes it the cohort date. loyalty_tier and status hold current values only, not their history."
}

// Migrate creates or updates the Customer Service schema.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return database.AutoMigrate(ctx, db, &Customer{})
}
