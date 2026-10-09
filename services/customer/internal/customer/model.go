// Package customer is the Customer Service domain: who the user is, where
// they are, their loyalty tier, and whether their account is in good standing.
package customer

import "github.com/poph2/datastack/pkgs/database"

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

// Customer is a row in the customers table.
type Customer struct {
	database.Model
	Email       string
	Country     string
	LoyaltyTier LoyaltyTier
	Status      Status
}
