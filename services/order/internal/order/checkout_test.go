package order

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeStore is an in-memory Store and StuckStore with Repository's guarded
// transition semantics.
type fakeStore struct {
	mu     sync.Mutex
	orders map[uint64]Order
	nextID uint64
}

func newFakeStore() *fakeStore {
	return &fakeStore{orders: map[uint64]Order{}, nextID: 1}
}

func (s *fakeStore) Create(ctx context.Context, o *Order) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o.ID = s.nextID
	s.nextID++
	s.orders[o.ID] = *o
	return nil
}

func (s *fakeStore) Get(_ context.Context, id uint64) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return Order{}, ErrNotFound
	}
	return o, nil
}

func (s *fakeStore) Apply(ctx context.Context, id uint64, t Transition) (Order, error) {
	if err := ctx.Err(); err != nil {
		return Order{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return Order{}, ErrNotFound
	}
	if o.Status != t.From {
		return Order{}, ErrStatusChanged
	}
	o.Status = t.To
	if t.UnitPrice != nil {
		o.UnitPrice = t.UnitPrice
	}
	if t.TotalAmount != nil {
		o.TotalAmount = t.TotalAmount
	}
	if t.FailureReason != nil {
		o.FailureReason = t.FailureReason
	}
	s.orders[id] = o
	return o, nil
}

// fakePeers records every call and returns configured results.
type fakePeers struct {
	customer    Customer
	customerErr error
	unitPrice   decimal.Decimal
	reserveErr  error
	chargeErr   error
	shipErr     error
	marketErr   error

	reserved, released, charged, shipped, converted int
	chargedAmount                                   decimal.Decimal
	convertedAmount                                 decimal.Decimal
}

func newFakePeers() *fakePeers {
	return &fakePeers{
		customer:  Customer{Country: "US", LoyaltyTier: "GOLD", Status: customerActive},
		unitPrice: decimal.RequireFromString("49.99"),
	}
}

func (p *fakePeers) Get(context.Context, uint64) (Customer, error) { return p.customer, p.customerErr }

func (p *fakePeers) Reserve(context.Context, string, int) (decimal.Decimal, error) {
	p.reserved++
	return p.unitPrice, p.reserveErr
}

func (p *fakePeers) Release(context.Context, string, int) error {
	p.released++
	return nil
}

func (p *fakePeers) Charge(_ context.Context, _ uint64, amount decimal.Decimal) error {
	p.charged++
	p.chargedAmount = amount
	return p.chargeErr
}

func (p *fakePeers) RecordConversion(_ context.Context, _, _ uint64, amount decimal.Decimal) error {
	p.converted++
	p.convertedAmount = amount
	return p.marketErr
}

func (p *fakePeers) Create(context.Context, uint64, Customer) (string, error) {
	p.shipped++
	return "1ZTRACK", p.shipErr
}

func (p *fakePeers) asPeers() Peers {
	return Peers{Customers: p, Inventory: p, Payments: p, Marketing: p, Shipments: p}
}

func runCheckout(t *testing.T, peers *fakePeers, req CheckoutRequest) (CheckoutResult, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	result, err := NewCheckout(store, peers.asPeers(), discardLogger).Run(context.Background(), req)
	require.NoError(t, err)
	return result, store
}

func attribution(id uint64) *uint64 { return &id }

func TestCheckoutHappyPath(t *testing.T) {
	peers := newFakePeers()

	result, _ := runCheckout(t, peers, CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 3, AttributionID: attribution(99)})

	assert.Equal(t, StatusShipped, result.Order.Status)
	assert.Nil(t, result.Order.FailureReason)
	assert.Equal(t, "1ZTRACK", result.TrackingNumber)
	assert.Equal(t, "49.99", result.Order.UnitPrice.StringFixed(2))
	assert.Equal(t, "149.97", result.Order.TotalAmount.StringFixed(2))
	assert.Equal(t, "149.97", peers.chargedAmount.StringFixed(2), "charges unit price x quantity")
	assert.Equal(t, "149.97", peers.convertedAmount.StringFixed(2))
	assert.Equal(t, 1, peers.converted)
	assert.Zero(t, peers.released)
}

func TestCheckoutFailures(t *testing.T) {
	cases := map[string]struct {
		setup        func(p *fakePeers)
		wantReason   FailureReason
		wantReserve  int
		wantCharge   int
		wantReleased int
	}{
		"unknown customer": {
			setup:      func(p *fakePeers) { p.customerErr = ErrCustomerNotFound },
			wantReason: ReasonCustomerInvalid,
		},
		"suspended customer": {
			setup:      func(p *fakePeers) { p.customer.Status = "SUSPENDED" },
			wantReason: ReasonCustomerSuspended,
		},
		"customer service down": {
			setup:      func(p *fakePeers) { p.customerErr = errors.New("connection refused") },
			wantReason: ReasonUpstreamError,
		},
		"out of stock never touches payment": {
			setup:       func(p *fakePeers) { p.reserveErr = ErrOutOfStock },
			wantReason:  ReasonOutOfStock,
			wantReserve: 1,
		},
		"unknown product": {
			setup:       func(p *fakePeers) { p.reserveErr = ErrProductNotFound },
			wantReason:  ReasonProductNotFound,
			wantReserve: 1,
		},
		"declined payment releases stock": {
			setup:        func(p *fakePeers) { p.chargeErr = ErrPaymentDeclined },
			wantReason:   ReasonPaymentDeclined,
			wantReserve:  1,
			wantCharge:   1,
			wantReleased: 1,
		},
		"payment service down releases stock": {
			setup:        func(p *fakePeers) { p.chargeErr = errors.New("timeout") },
			wantReason:   ReasonUpstreamError,
			wantReserve:  1,
			wantCharge:   1,
			wantReleased: 1,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			peers := newFakePeers()
			tc.setup(peers)

			result, store := runCheckout(t, peers, CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 1, AttributionID: attribution(5)})

			assert.Equal(t, StatusFailed, result.Order.Status)
			require.NotNil(t, result.Order.FailureReason)
			assert.Equal(t, tc.wantReason, *result.Order.FailureReason)
			assert.Equal(t, tc.wantReserve, peers.reserved)
			assert.Equal(t, tc.wantCharge, peers.charged)
			assert.Equal(t, tc.wantReleased, peers.released)
			assert.Zero(t, peers.converted, "failed orders are never reported as conversions")
			assert.Zero(t, peers.shipped)

			stored, err := store.Get(context.Background(), result.Order.ID)
			require.NoError(t, err)
			assert.Equal(t, StatusFailed, stored.Status, "every attempt is recorded")
		})
	}
}

func TestCheckoutWithoutAttributionSkipsMarketing(t *testing.T) {
	peers := newFakePeers()

	result, _ := runCheckout(t, peers, CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 1})

	assert.Equal(t, StatusShipped, result.Order.Status)
	assert.Zero(t, peers.converted)
}

func TestCheckoutMarketingFailureDoesNotFailTheOrder(t *testing.T) {
	peers := newFakePeers()
	peers.marketErr = errors.New("marketing down")

	result, _ := runCheckout(t, peers, CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 1, AttributionID: attribution(5)})

	assert.Equal(t, StatusShipped, result.Order.Status)
}

func TestCheckoutShipmentFailureLeavesOrderPaid(t *testing.T) {
	peers := newFakePeers()
	peers.shipErr = errors.New("shipment down")

	result, _ := runCheckout(t, peers, CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 1})

	assert.Equal(t, StatusPaid, result.Order.Status, "paid but not shipped: the reconciler retries it")
	assert.Empty(t, result.TrackingNumber)
	assert.Zero(t, peers.released, "a paid order keeps its stock")
}

func TestCheckoutCompletesWhenTheCallerDisconnects(t *testing.T) {
	peers := newFakePeers()
	store := newFakeStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client has already gone away

	result, err := NewCheckout(store, peers.asPeers(), discardLogger).Run(ctx, CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 1})

	require.NoError(t, err)
	assert.Equal(t, StatusShipped, result.Order.Status, "a started checkout is never stranded in PENDING")
}
