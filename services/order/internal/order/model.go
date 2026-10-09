// Package order is the Order Service domain: the order record and the
// checkout that orchestrates every other service to fill it.
package order

import (
	"context"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
)

// moneyDecimals is the number of decimal places amounts are rendered with.
const moneyDecimals = 2

// Status is where an order is in its lifecycle:
// PENDING -> PAID -> SHIPPED, or PENDING -> FAILED.
type Status string

const (
	StatusPending Status = "PENDING"
	StatusPaid    Status = "PAID"
	StatusFailed  Status = "FAILED"
	StatusShipped Status = "SHIPPED"
)

// FailureReason records why a FAILED order failed. The values are the error
// codes returned by the services the checkout calls, so dashboards can show
// exactly where the business loses money.
type FailureReason string

const (
	ReasonCustomerInvalid   FailureReason = "CUSTOMER_INVALID"
	ReasonCustomerSuspended FailureReason = "CUSTOMER_SUSPENDED"
	ReasonOutOfStock        FailureReason = "OUT_OF_STOCK"
	ReasonProductNotFound   FailureReason = "PRODUCT_NOT_FOUND"
	ReasonPaymentDeclined   FailureReason = "PAYMENT_DECLINED"
	ReasonUpstreamError     FailureReason = "UPSTREAM_ERROR"
)

// Order is a row in the orders table. The gorm tags are the schema.
// UserID and AttributionID are logical references into other services'
// databases, so they aren't foreign keys.
type Order struct {
	database.Model
	UserID        uint64  `gorm:"not null"`
	AttributionID *uint64 // nil for organic traffic
	ProductID     string  `gorm:"size:50;not null"`
	Quantity      int     `gorm:"not null;check:chk_orders_quantity,quantity >= 1"`
	// Set once inventory has reserved the stock and quoted a price.
	UnitPrice   *decimal.Decimal `gorm:"type:decimal(10,2)"`
	TotalAmount *decimal.Decimal `gorm:"type:decimal(10,2)"`
	// The reconciler looks for orders stuck in PAID, so status gets an index.
	Status        Status         `gorm:"size:20;not null;index;check:chk_orders_status,status IN ('PENDING','PAID','FAILED','SHIPPED')"`
	FailureReason *FailureReason `gorm:"size:50;check:chk_orders_failure_reason,failure_reason IN ('CUSTOMER_INVALID','CUSTOMER_SUSPENDED','OUT_OF_STOCK','PRODUCT_NOT_FOUND','PAYMENT_DECLINED','UPSTREAM_ERROR')"`
}

// Migrate creates or updates the Order Service schema.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return database.AutoMigrate(ctx, db, &Order{})
}
