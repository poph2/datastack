package inventory
// Package inventory is the Inventory microservice: it owns the product catalog and stock levels.
package inventory

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
	serviceName  = "inventory"
	databaseName = "inventory"
)

// Product is a sellable item and its current stock.
type Product struct {
	gorm.Model
	SKU           string  `gorm:"not null;uniqueIndex"`
	Name          string  `gorm:"not null"`
	StockQuantity int     `gorm:"not null"`
	UnitPrice     float64 `gorm:"type:numeric(10,2);not null"`
}

// Service consumes RestockProduct commands and writes them to the inventory database.
type Service struct {
	db       *gorm.DB
	registry *registry.Registry
	commands chan messages.RestockProduct
}

// Start connects to the inventory database, migrates it, and begins consuming commands.
func Start(ctx context.Context, dbConfig database.Config, reg *registry.Registry) (*Service, error) {
	db, err := database.Open(dbConfig, databaseName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", serviceName, err)
	}
	if err := db.AutoMigrate(&Product{}); err != nil {
		return nil, fmt.Errorf("%s: migrating: %w", serviceName, err)
	}

	s := &Service{db: db, registry: reg, commands: make(chan messages.RestockProduct, messages.ChannelBuffer)}
	if err := s.loadExistingProducts(); err != nil {
		return nil, err
	}

	go s.run(ctx)
	return s, nil
}

// Commands is where the orchestrator sends RestockProduct commands.
func (s *Service) Commands() chan<- messages.RestockProduct {
	return s.commands
}

func (s *Service) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-s.commands:
			s.restock(cmd)
		}
	}
}

func (s *Service) restock(cmd messages.RestockProduct) {
	product, err := s.findOrCreateProduct(cmd)
	if err == nil {
		err = s.db.Model(&product).Update("stock_quantity", gorm.Expr("stock_quantity + ?", cmd.Quantity)).Error
	}
	metrics.RecordProcessed(serviceName, err)
	if err != nil {
		log.Printf("%s: restocking %s: %v", serviceName, cmd.SKU, err)
		return
	}
	s.registry.AddProduct(registry.Product{ID: product.ID, SKU: product.SKU, UnitPrice: product.UnitPrice})
}

func (s *Service) findOrCreateProduct(cmd messages.RestockProduct) (Product, error) {
	var product Product
	err := s.db.
		Where(Product{SKU: cmd.SKU}).
		Attrs(Product{Name: cmd.Name, UnitPrice: cmd.UnitPrice}).
		FirstOrCreate(&product).Error
	return product, err
}

func (s *Service) loadExistingProducts() error {
	var products []Product
	if err := s.db.Select("id", "sku", "unit_price").Find(&products).Error; err != nil {
		return fmt.Errorf("%s: loading existing products: %w", serviceName, err)
	}
	for _, p := range products {
		s.registry.AddProduct(registry.Product{ID: p.ID, SKU: p.SKU, UnitPrice: p.UnitPrice})
	}
	return nil
}
