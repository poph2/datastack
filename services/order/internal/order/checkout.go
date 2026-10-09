package order

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// The checkout's dependencies, one small interface per peer service, so tests
// can fake each one. The *Client types in clients.go implement them.
type (
	// Store persists orders. *Repository implements it.
	Store interface {
		Create(ctx context.Context, o *Order) error
		Get(ctx context.Context, id uint64) (Order, error)
		Apply(ctx context.Context, id uint64, t Transition) (Order, error)
	}
	Customers interface {
		Get(ctx context.Context, userID uint64) (Customer, error)
	}
	Inventory interface {
		Reserve(ctx context.Context, productID string, quantity int) (decimal.Decimal, error)
		Release(ctx context.Context, productID string, quantity int) error
	}
	Payments interface {
		Charge(ctx context.Context, orderID uint64, amount decimal.Decimal) error
	}
	Marketing interface {
		RecordConversion(ctx context.Context, attributionID, orderID uint64, amount decimal.Decimal) error
	}
	Shipments interface {
		Create(ctx context.Context, orderID uint64, customer Customer) (string, error)
	}
)

// Peers bundles the services the checkout calls.
type Peers struct {
	Customers Customers
	Inventory Inventory
	Payments  Payments
	Marketing Marketing
	Shipments Shipments
}

// CheckoutRequest is one purchase.
type CheckoutRequest struct {
	UserID        uint64
	ProductID     string
	Quantity      int
	AttributionID *uint64
}

// CheckoutResult is the order as the checkout left it. A business failure
// (declined card, no stock, ...) is not an error: it's an order in FAILED
// with a FailureReason.
type CheckoutResult struct {
	Order          Order
	TrackingNumber string
}

// Checkout orchestrates a purchase across every other service.
type Checkout struct {
	store  Store
	peers  Peers
	logger *slog.Logger
}

// NewCheckout returns a Checkout.
func NewCheckout(store Store, peers Peers, logger *slog.Logger) *Checkout {
	return &Checkout{store: store, peers: peers, logger: logger}
}

// Run performs the checkout:
//
//  1. Record the order as PENDING, so every attempt is counted, failures included.
//  2. Validate the customer (must exist and be ACTIVE).
//  3. Reserve stock, which also prices the order.
//  4. Charge payment. A decline releases the stock.
//  5. Mark PAID, then report the conversion to marketing (best effort).
//  6. Request shipment, then mark SHIPPED. If that fails the order stays PAID,
//     and the reconciler retries it.
//
// It returns an error only when the Order Service's own database fails.
//
// Once started, a checkout runs to completion even if the caller disconnects:
// stopping halfway would strand the order in PENDING with its stock reserved.
// So ctx's cancellation is dropped (its values, including the trace, are
// kept). Every peer call has its own timeout, so the checkout still can't
// hang.
func (c *Checkout) Run(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	ctx = context.WithoutCancel(ctx)

	order := Order{
		UserID:        req.UserID,
		AttributionID: req.AttributionID,
		ProductID:     req.ProductID,
		Quantity:      req.Quantity,
		Status:        StatusPending,
	}
	if err := c.store.Create(ctx, &order); err != nil {
		return CheckoutResult{}, err
	}
	annotateSpan(ctx, order)

	customer, err := c.peers.Customers.Get(ctx, req.UserID)
	if err != nil {
		return c.fail(ctx, order, reasonFor(err), err)
	}
	if customer.Status != customerActive {
		return c.fail(ctx, order, ReasonCustomerSuspended, fmt.Errorf("customer status is %s", customer.Status))
	}

	unitPrice, err := c.peers.Inventory.Reserve(ctx, req.ProductID, req.Quantity)
	if err != nil {
		return c.fail(ctx, order, reasonFor(err), err)
	}
	total := unitPrice.Mul(decimal.NewFromInt(int64(req.Quantity)))

	order, err = c.store.Apply(ctx, order.ID, Transition{
		From: StatusPending, To: StatusPending, UnitPrice: &unitPrice, TotalAmount: &total,
	})
	if err != nil {
		c.release(ctx, req)
		return CheckoutResult{}, err
	}

	if err := c.peers.Payments.Charge(ctx, order.ID, total); err != nil {
		c.release(ctx, req)
		return c.fail(ctx, order, reasonFor(err), err)
	}

	order, err = c.store.Apply(ctx, order.ID, Transition{From: StatusPending, To: StatusPaid})
	if err != nil {
		return CheckoutResult{}, err
	}
	revenueTotal.Add(total.InexactFloat64())

	c.recordConversion(ctx, order, total)
	return c.ship(ctx, order, customer)
}

// ship requests the shipment and marks the order SHIPPED. The reconciler
// also uses it for orders stuck in PAID.
func (c *Checkout) ship(ctx context.Context, order Order, customer Customer) (CheckoutResult, error) {
	tracking, err := c.peers.Shipments.Create(ctx, order.ID, customer)
	if err != nil {
		c.logger.WarnContext(ctx, "shipment request failed, order stays PAID for the reconciler",
			"order_id", order.ID, "error", err)
		checkoutsTotal.WithLabelValues(string(StatusPaid), "").Inc()
		return CheckoutResult{Order: order}, nil
	}

	shipped, err := c.store.Apply(ctx, order.ID, Transition{From: StatusPaid, To: StatusShipped})
	if err != nil {
		return CheckoutResult{}, err
	}
	checkoutsTotal.WithLabelValues(string(StatusShipped), "").Inc()
	annotateSpan(ctx, shipped)
	return CheckoutResult{Order: shipped, TrackingNumber: tracking}, nil
}

// fail marks the order FAILED with reason. cause is logged; the caller's
// result carries only the reason.
func (c *Checkout) fail(ctx context.Context, order Order, reason FailureReason, cause error) (CheckoutResult, error) {
	failed, err := c.store.Apply(ctx, order.ID, Transition{From: StatusPending, To: StatusFailed, FailureReason: &reason})
	if err != nil {
		return CheckoutResult{}, err
	}

	checkoutsTotal.WithLabelValues(string(StatusFailed), string(reason)).Inc()
	level := slog.LevelInfo
	if reason == ReasonUpstreamError {
		level = slog.LevelError
	}
	c.logger.Log(ctx, level, "checkout failed", "order_id", order.ID, "reason", reason, "cause", cause)
	annotateSpan(ctx, failed)
	return CheckoutResult{Order: failed}, nil
}

// release gives reserved stock back. If it fails, the stock is lost until the
// Inventory Service's restock loop tops it up, so it is logged loudly but
// doesn't change the checkout's outcome.
func (c *Checkout) release(ctx context.Context, req CheckoutRequest) {
	if err := c.peers.Inventory.Release(ctx, req.ProductID, req.Quantity); err != nil {
		c.logger.ErrorContext(ctx, "releasing reserved stock failed",
			"product_id", req.ProductID, "quantity", req.Quantity, "error", err)
	}
}

// recordConversion tells marketing which ad click produced this sale. It's
// not on the critical path: a failure only leaves the click unconverted.
func (c *Checkout) recordConversion(ctx context.Context, order Order, total decimal.Decimal) {
	if order.AttributionID == nil {
		return
	}
	err := c.peers.Marketing.RecordConversion(ctx, *order.AttributionID, order.ID, total)
	if err != nil {
		c.logger.WarnContext(ctx, "recording marketing conversion failed",
			"order_id", order.ID, "attribution_id", *order.AttributionID, "error", err)
	}
}

// reasonFor maps a peer error to the order's failure reason. Anything
// unexpected (timeouts, 5xx, unknown codes) is an upstream error.
func reasonFor(err error) FailureReason {
	switch {
	case errors.Is(err, ErrCustomerNotFound):
		return ReasonCustomerInvalid
	case errors.Is(err, ErrOutOfStock):
		return ReasonOutOfStock
	case errors.Is(err, ErrProductNotFound):
		return ReasonProductNotFound
	case errors.Is(err, ErrPaymentDeclined):
		return ReasonPaymentDeclined
	default:
		return ReasonUpstreamError
	}
}

func annotateSpan(ctx context.Context, order Order) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int64("order.id", int64(order.ID)),
		attribute.String("order.status", string(order.Status)),
		attribute.Int64("customer.id", int64(order.UserID)),
		attribute.String("product.id", order.ProductID),
	)
	if order.TotalAmount != nil {
		span.SetAttributes(attribute.String("order.total_amount", order.TotalAmount.StringFixed(moneyDecimals)))
	}
	if order.AttributionID != nil {
		span.SetAttributes(attribute.Int64("attribution.id", int64(*order.AttributionID)))
	}
	if order.FailureReason != nil {
		span.SetAttributes(attribute.String("order.failure_reason", string(*order.FailureReason)))
	}
}
