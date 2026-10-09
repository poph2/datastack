// Package payment is the Payment Service domain: a simulated card gateway
// that charges orders, declines a configurable share of them on purpose, and
// records every attempt.
package payment

import (
	"context"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
)

// moneyDecimals is the number of decimal places amounts are stored and
// rendered with. Money is a shopspring decimal everywhere (never float64) and
// is serialised as a fixed-point string such as "99.99".
const moneyDecimals = 2

// Method is how the customer pays.
type Method string

const (
	MethodCreditCard Method = "CREDIT_CARD"
	MethodPayPal     Method = "PAYPAL"
)

// Status is the state of the money. A payment is SUCCESS or DECLINED when it
// is recorded, and only a SUCCESS can later become REFUNDED.
type Status string

const (
	StatusSuccess  Status = "SUCCESS"
	StatusDeclined Status = "DECLINED"
	StatusRefunded Status = "REFUNDED"
)

// DeclineReason says why the simulated gateway declined a charge, so the
// revenue-leakage dashboard can break declines down by cause.
type DeclineReason string

const (
	ReasonCardDeclined      DeclineReason = "CARD_DECLINED"
	ReasonInsufficientFunds DeclineReason = "INSUFFICIENT_FUNDS"
	ReasonFraudSuspected    DeclineReason = "FRAUD_SUSPECTED"
)

// Payment is a row in the payments table: one charge attempt. Declines are
// recorded too, because they are facts the analytics depend on. The gorm tags
// are the schema: AutoMigrate creates the columns, CHECK constraints and
// indexes from them.
type Payment struct {
	database.Model
	// The partial unique index is the double-charge guard: an order may have
	// any number of DECLINED attempts but at most one SUCCESS, even when
	// concurrent requests race. A refunded payment no longer counts, so the
	// order can be charged again.
	OrderID uint64          `gorm:"not null;uniqueIndex:idx_payments_order_id_success,where:status = 'SUCCESS' AND deleted_at IS NULL"`
	Amount  decimal.Decimal `gorm:"type:decimal(10,2);not null;check:chk_payments_amount,amount > 0"`
	Method  Method          `gorm:"size:20;not null;check:chk_payments_method,method IN ('CREDIT_CARD','PAYPAL')"`
	Status  Status          `gorm:"size:20;not null;check:chk_payments_status,status IN ('SUCCESS','DECLINED','REFUNDED')"`
	// Set if and only if the payment was declined. The CHECK enforces both the
	// allowed values and that pairing.
	DeclineReason *DeclineReason `gorm:"size:50;check:chk_payments_decline_reason,(decline_reason IS NOT NULL) = (status = 'DECLINED') AND (decline_reason IS NULL OR decline_reason IN ('CARD_DECLINED','INSUFFICIENT_FUNDS','FRAUD_SUSPECTED'))"`
}

// Migrate creates or updates the Payment Service schema. There is no seed
// data: every row comes from a real charge attempt.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return database.AutoMigrate(ctx, db, &Payment{})
}
