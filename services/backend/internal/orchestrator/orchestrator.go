// Package orchestrator drives the simulated business: it runs the clock and sends commands to every microservice.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/poph2/datastack/services/backend/internal/clock"
	"github.com/poph2/datastack/services/backend/internal/messages"
	"github.com/poph2/datastack/services/backend/internal/metrics"
	"github.com/poph2/datastack/services/backend/internal/registry"
)

// ErrCaughtUp is returned when starting an orchestrator whose timeline has already reached today.
var ErrCaughtUp = errors.New("orchestrator has already caught up to today")

// tickInterval is one simulated day, matching clock.SimulatedDayPerSecond.
const tickInterval = time.Second

// Targets are the microservice inboxes the orchestrator sends commands to.
type Targets struct {
	Customers chan<- messages.CreateCustomer
	Products  chan<- messages.RestockProduct
	Orders    chan<- messages.CreateOrder
	Payments  chan<- messages.ProcessPayment
}

// Orchestrator generates one simulated day of business activity per tick until it catches up to today.
type Orchestrator struct {
	targets   Targets
	registry  *registry.Registry
	statePath string

	mu      sync.Mutex
	state   State
	cancel  context.CancelFunc
	stopped chan struct{}
}

// New loads any saved progress from statePath; call Start or ResumeIfRunning to begin.
func New(targets Targets, reg *registry.Registry, statePath string) (*Orchestrator, error) {
	state, err := loadState(statePath)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: %w", err)
	}
	return &Orchestrator{targets: targets, registry: reg, statePath: statePath, state: state}, nil
}

// ResumeIfRunning restarts the timeline if it was running when the process last stopped.
func (o *Orchestrator) ResumeIfRunning() error {
	if !o.Status().Running {
		return nil
	}
	return o.Start()
}

// Start runs the clock from where it left off (or January 1st) and begins sending commands.
func (o *Orchestrator) Start() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.cancel != nil {
		return nil
	}
	if o.state.CaughtUp {
		return ErrCaughtUp
	}

	from := o.state.SimulatedTime
	if from.IsZero() {
		from = startOfYear(time.Now())
	}
	clock.Start(from)

	ctx, cancel := context.WithCancel(context.Background())
	o.cancel, o.stopped = cancel, make(chan struct{})
	o.state.Running = true
	o.saveLocked()

	go o.run(ctx, o.stopped)
	log.Printf("orchestrator: started at simulated time %s", from.Format(time.DateOnly))
	return nil
}

// Stop pauses the timeline and waits for the current day to finish dispatching.
func (o *Orchestrator) Stop() error {
	o.mu.Lock()
	cancel, stopped := o.cancel, o.stopped
	o.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()
	<-stopped
	return nil
}

// Status returns a snapshot of the orchestrator's persisted progress.
func (o *Orchestrator) Status() State {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state
}

func (o *Orchestrator) run(ctx context.Context, stopped chan struct{}) {
	defer close(stopped)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.halt(false)
			return
		case <-ticker.C:
			if clock.CaughtUp() {
				o.halt(true)
				return
			}
			o.simulateDay(ctx)
		}
	}
}

// halt records why the loop ended; after catching up the clock keeps tracking real time.
func (o *Orchestrator) halt(caughtUp bool) {
	if !caughtUp {
		clock.Freeze()
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != nil {
		o.cancel()
	}
	o.cancel, o.stopped = nil, nil
	o.state.Running = false
	o.state.CaughtUp = o.state.CaughtUp || caughtUp
	o.state.SimulatedTime = clock.Now()
	o.saveLocked()

	if caughtUp {
		log.Printf("orchestrator: historical backfill complete after %d simulated days", o.state.Ticks)
	} else {
		log.Printf("orchestrator: stopped at simulated time %s", o.state.SimulatedTime.Format(time.DateOnly))
	}
}

func (o *Orchestrator) simulateDay(ctx context.Context) {
	o.sendNewCustomers(ctx)
	o.sendRestocks(ctx)
	o.sendOrders(ctx)
	o.sendPayments(ctx)
	o.recordTick()
}

func (o *Orchestrator) sendNewCustomers(ctx context.Context) {
	for range between(minNewCustomers, maxNewCustomers) {
		send(ctx, o.targets.Customers, newCustomer(), "customer", "CreateCustomer")
	}
}

func (o *Orchestrator) sendRestocks(ctx context.Context) {
	_, knownProducts, _ := o.registry.Counts()
	for range between(minRestocks, maxRestocks) {
		existing, ok := o.registry.RandomProduct()
		if ok && rand.Float64() >= newProductChance {
			restock := messages.RestockProduct{SKU: existing.SKU, Quantity: between(20, 100)}
			send(ctx, o.targets.Products, restock, "inventory", "RestockProduct")
			continue
		}
		knownProducts++
		send(ctx, o.targets.Products, newProduct(knownProducts), "inventory", "RestockProduct")
	}
}

func (o *Orchestrator) sendOrders(ctx context.Context) {
	for range between(minOrders, maxOrders) {
		customerID, hasCustomer := o.registry.RandomCustomer()
		product, hasProduct := o.registry.RandomProduct()
		if !hasCustomer || !hasProduct {
			return
		}
		order := messages.CreateOrder{
			CustomerID: customerID,
			ProductID:  product.ID,
			Quantity:   between(1, maxOrderQuantity),
			UnitPrice:  product.UnitPrice,
		}
		send(ctx, o.targets.Orders, order, "sales", "CreateOrder")
	}
}

func (o *Orchestrator) sendPayments(ctx context.Context) {
	for range maxPaymentsPerDay {
		invoiceID, ok := o.registry.TakePendingInvoice()
		if !ok {
			return
		}
		payment := messages.ProcessPayment{InvoiceID: invoiceID, Succeeded: rand.Float64() < paymentSuccessRate}
		send(ctx, o.targets.Payments, payment, "invoicing", "ProcessPayment")
	}
}

func (o *Orchestrator) recordTick() {
	now := clock.Now()
	customers, products, pendingInvoices := o.registry.Counts()

	metrics.OrchestratorTicks.Inc()
	metrics.SimulatedTime.Set(float64(now.Unix()))
	metrics.StateCacheSize.WithLabelValues("customer").Set(float64(customers))
	metrics.StateCacheSize.WithLabelValues("inventory").Set(float64(products))
	metrics.StateCacheSize.WithLabelValues("pending_invoice").Set(float64(pendingInvoices))

	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.Ticks++
	o.state.SimulatedTime = now
	o.saveLocked()
}

func (o *Orchestrator) saveLocked() {
	if err := saveState(o.statePath, o.state); err != nil {
		log.Printf("orchestrator: saving state: %v", err)
	}
}

func send[T any](ctx context.Context, inbox chan<- T, msg T, targetService, commandType string) {
	select {
	case inbox <- msg:
		metrics.CommandsDispatched.WithLabelValues(targetService, commandType).Inc()
	case <-ctx.Done():
	}
}

func startOfYear(t time.Time) time.Time {
	return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
}
