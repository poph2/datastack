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
	UserID        uint64           `gorm:"not null;comment:Customer who checked out. A logical reference into the Customer Service, not an enforced foreign key. [ref: customers.id]"`
	AttributionID *uint64          `gorm:"comment:Ad click that led to this checkout. NULL for organic traffic that arrived without an ad click. [ref: attributions.id]"`
	ProductID     string           `gorm:"size:50;not null;comment:Product bought. Every order is for exactly one product. [ref: inventory.product_id]"`
	Quantity      int              `gorm:"not null;check:chk_orders_quantity,quantity >= 1;comment:Number of units bought, 1 or more."`
	UnitPrice     *decimal.Decimal `gorm:"type:decimal(10,2);comment:Price per unit in USD charged at checkout, copied from inventory.price. NULL if the checkout failed before stock was reserved."`
	TotalAmount   *decimal.Decimal `gorm:"type:decimal(10,2);comment:Order value in USD: unit_price times quantity. NULL if the checkout failed before stock was reserved. Counts as revenue only when status is PAID or SHIPPED."`
	Status        Status           `gorm:"size:20;not null;index;check:chk_orders_status,status IN ('PENDING','PAID','FAILED','SHIPPED');comment:Lifecycle state: PENDING (checkout in progress), PAID (charged, shipment not yet requested), SHIPPED (charged and handed to fulfillment, see shipments.shipped_at for when the box left) or FAILED (see failure_reason)."`
	FailureReason *FailureReason   `gorm:"size:50;check:chk_orders_failure_reason,failure_reason IN ('CUSTOMER_INVALID','CUSTOMER_SUSPENDED','OUT_OF_STOCK','PRODUCT_NOT_FOUND','PAYMENT_DECLINED','UPSTREAM_ERROR');comment:Why a FAILED checkout failed, NULL otherwise: CUSTOMER_INVALID (unknown customer), CUSTOMER_SUSPENDED, OUT_OF_STOCK, PRODUCT_NOT_FOUND, PAYMENT_DECLINED (see payments.decline_reason) or UPSTREAM_ERROR (a dependency was unavailable)."`
}

// TableComment documents the table for the semantic layer (see
// database.TableCommenter).
func (Order) TableComment() string {
	return "Checkout attempts (Order Service): one row per checkout, failed ones included. The central fact table: user_id joins customers.id, product_id joins inventory.product_id, attribution_id joins attributions.id, and payments.order_id and shipments.order_id join back to orders.id. created_at is when the checkout happened. Revenue is the sum of total_amount over orders with status PAID or SHIPPED. Checkout success rate is SHIPPED orders divided by all orders."
}

// Migrate creates or updates the Order Service schema.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return database.AutoMigrate(ctx, db, &Order{})
}
