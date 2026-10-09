package customer
// Package customer is the Customer microservice: it owns the customer database and its accounts.
package customer

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
	serviceName  = "customer"
	databaseName = "customer"
)

// Customer is a registered shopper account.
type Customer struct {
	gorm.Model
	FullName string `gorm:"not null"`
	Email    string `gorm:"not null;uniqueIndex"`
}

// Service consumes CreateCustomer commands and writes them to the customer database.
type Service struct {
	db       *gorm.DB
	registry *registry.Registry
	commands chan messages.CreateCustomer
}

// Start connects to the customer database, migrates it, and begins consuming commands.
func Start(ctx context.Context, dbConfig database.Config, reg *registry.Registry) (*Service, error) {
	db, err := database.Open(dbConfig, databaseName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	if err := db.AutoMigrate(&Customer{}); err != nil {
		return nil, fmt.Errorf("%s: migrating: %w", serviceName, err)
	}

	s := &Service{db: db, registry: reg, commands: make(chan messages.CreateCustomer, messages.ChannelBuffer)}
	if err := s.loadExistingCustomers(); err != nil {
		return nil, err
	}

	go s.run(ctx)
	return s, nil
}

// Commands is where the orchestrator sends CreateCustomer commands.
func (s *Service) Commands() chan<- messages.CreateCustomer {
	return s.commands
}

func (s *Service) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-s.commands:
			s.createCustomer(cmd)
		}
	}
}

func (s *Service) createCustomer(cmd messages.CreateCustomer) {
	customer := Customer{FullName: cmd.FullName, Email: cmd.Email}
	err := s.db.Create(&customer).Error
	metrics.RecordProcessed(serviceName, err)
	if err != nil {
		log.Printf("%s: creating customer %q: %v", serviceName, cmd.Email, err)
		return
	}
	s.registry.AddCustomer(customer.ID)
}

func (s *Service) loadExistingCustomers() error {
	var ids []uint
	if err := s.db.Model(&Customer{}).Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("%s: loading existing customers: %w", serviceName, err)
	}
	for _, id := range ids {
		s.registry.AddCustomer(id)
	}
	return nil
}
