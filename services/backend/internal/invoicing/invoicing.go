// Package invoicing is the Invoicing microservice: it bills orders and records payment outcomes.
package invoicing

import (
	"context"
	"fmt"
	"log"

	"github.com/poph2/datastack/services/backend/internal/database"
	"github.com/poph2/datastack/services/backend/internal/messages"
	"github.com/poph2/datastack/services/backend/internal/metrics"
	"github.com/poph2/datastack/services/backend/internal/registry"
	"gorm.io/gorm"
)

const (
	serviceName  = "invoicing"
	databaseName = "invoicing"

	statusPending = "PENDING"
	statusPaid    = "PAID"
	statusFailed  = "FAILED"
)

// Invoice bills one order and tracks whether it has been paid.
type Invoice struct {
	gorm.Model
	OrderID       uint    `gorm:"not null;uniqueIndex"`
	CustomerID    uint    `gorm:"not null;index"`
	Amount        float64 `gorm:"type:numeric(12,2);not null"`
	PaymentStatus string  `gorm:"not null;index"`
}

// Service consumes OrderCreated events from Sales and ProcessPayment commands from the orchestrator.
type Service struct {
	db          *gorm.DB
	registry    *registry.Registry
	orderEvents chan messages.OrderCreated
	payments    chan messages.ProcessPayment
}

// Start connects to the invoicing database, migrates it, and begins consuming messages.
func Start(ctx context.Context, dbConfig database.Config, reg *registry.Registry) (*Service, error) {
	db, err := database.Open(dbConfig, databaseName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	if err := db.AutoMigrate(&Invoice{}); err != nil {
		return nil, fmt.Errorf("%s: migrating: %w", serviceName, err)
	}

	s := &Service{
		db:          db,
		registry:    reg,
		orderEvents: make(chan messages.OrderCreated, messages.ChannelBuffer),
		payments:    make(chan messages.ProcessPayment, messages.ChannelBuffer),
	}
	if err := s.loadPendingInvoices(); err != nil {
		return nil, err
	}

	go s.run(ctx)
	return s, nil
}

// OrderEvents is where the Sales service sends OrderCreated events.
func (s *Service) OrderEvents() chan<- messages.OrderCreated {
	return s.orderEvents
}

// Payments is where the orchestrator sends ProcessPayment commands.
func (s *Service) Payments() chan<- messages.ProcessPayment {
	return s.payments
}

func (s *Service) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.orderEvents:
			s.createInvoice(event)
		case cmd := <-s.payments:
			s.recordPayment(cmd)
		}
	}
}

func (s *Service) createInvoice(event messages.OrderCreated) {
	invoice := Invoice{
		OrderID:       event.OrderID,
		CustomerID:    event.CustomerID,
		Amount:        event.Amount,
		PaymentStatus: statusPending,
	}
	err := s.db.Create(&invoice).Error
	metrics.RecordProcessed(serviceName, err)
	if err != nil {
		log.Printf("%s: invoicing order %d: %v", serviceName, event.OrderID, err)
		return
	}
	s.registry.AddPendingInvoice(invoice.ID)
}

func (s *Service) recordPayment(cmd messages.ProcessPayment) {
	status := statusFailed
	if cmd.Succeeded {
		status = statusPaid
	}
	err := s.db.Model(&Invoice{}).Where("id = ?", cmd.InvoiceID).Update("payment_status", status).Error
	metrics.RecordProcessed(serviceName, err)
	if err != nil {
		log.Printf("%s: recording payment for invoice %d: %v", serviceName, cmd.InvoiceID, err)
	}
}

func (s *Service) loadPendingInvoices() error {
	var ids []uint
	err := s.db.Model(&Invoice{}).Where("payment_status = ?", statusPending).Pluck("id", &ids).Error
	if err != nil {
		return fmt.Errorf("%s: loading pending invoices: %w", serviceName, err)
	}
	for _, id := range ids {
		s.registry.AddPendingInvoice(id)
	}
	return nil
}
