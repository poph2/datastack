// Package sales is the Sales microservice: it owns orders and announces each new one to Invoicing.
package sales

import (
	"context"
	"fmt"
	"log"

	"github.com/poph2/datastack/services/backend/internal/database"
	"github.com/poph2/datastack/services/backend/internal/messages"
	"github.com/poph2/datastack/services/backend/internal/metrics"
	"gorm.io/gorm"
)

const (
	serviceName  = "sales"
	databaseName = "sales"

	statusCompleted = "COMPLETED"
)

// Order is a completed checkout of one product by one customer.
type Order struct {
	gorm.Model
	CustomerID  uint    `gorm:"not null;index"`
	ProductID   uint    `gorm:"not null;index"`
	Quantity    int     `gorm:"not null"`
	TotalAmount float64 `gorm:"type:numeric(12,2);not null"`
	Status      string  `gorm:"not null"`
}

// Service consumes CreateOrder commands and emits OrderCreated events.
type Service struct {
	db          *gorm.DB
	commands    chan messages.CreateOrder
	orderEvents chan<- messages.OrderCreated
}

// Start connects to the sales database, migrates it, and begins consuming commands.
func Start(ctx context.Context, dbConfig database.Config, orderEvents chan<- messages.OrderCreated) (*Service, error) {
	db, err := database.Open(dbConfig, databaseName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	if err := db.AutoMigrate(&Order{}); err != nil {
		return nil, fmt.Errorf("%s: migrating: %w", serviceName, err)
	}

	s := &Service{
		db:          db,
		commands:    make(chan messages.CreateOrder, messages.ChannelBuffer),
		orderEvents: orderEvents,
	}

	go s.run(ctx)
	return s, nil
}

// Commands is where the orchestrator sends CreateOrder commands.
func (s *Service) Commands() chan<- messages.CreateOrder {
	return s.commands
}

func (s *Service) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-s.commands:
			s.createOrder(ctx, cmd)
		}
	}
}

func (s *Service) createOrder(ctx context.Context, cmd messages.CreateOrder) {
	order := Order{
		CustomerID:  cmd.CustomerID,
		ProductID:   cmd.ProductID,
		Quantity:    cmd.Quantity,
		TotalAmount: float64(cmd.Quantity) * cmd.UnitPrice,
		Status:      statusCompleted,
	}
	err := s.db.Create(&order).Error
	metrics.RecordProcessed(serviceName, err)
	if err != nil {
		log.Printf("%s: creating order for customer %d: %v", serviceName, cmd.CustomerID, err)
		return
	}

	event := messages.OrderCreated{OrderID: order.ID, CustomerID: order.CustomerID, Amount: order.TotalAmount}
	select {
	case s.orderEvents <- event:
	case <-ctx.Done():
	}
}
