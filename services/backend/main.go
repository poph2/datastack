// Command backend runs DataStack in one binary: the orchestrator, the four microservices, and the API.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/poph2/datastack/services/backend/internal/agent"
	"github.com/poph2/datastack/services/backend/internal/api"
	"github.com/poph2/datastack/services/backend/internal/control"
	"github.com/poph2/datastack/services/backend/internal/customer"
	"github.com/poph2/datastack/services/backend/internal/database"
	"github.com/poph2/datastack/services/backend/internal/ingestion"
	"github.com/poph2/datastack/services/backend/internal/inventory"
	"github.com/poph2/datastack/services/backend/internal/invoicing"
	"github.com/poph2/datastack/services/backend/internal/orchestrator"
	"github.com/poph2/datastack/services/backend/internal/registry"
	"github.com/poph2/datastack/services/backend/internal/sales"
)

func main() {
	addr := envOr("BACKEND_ADDR", ":8080")
	statePath := envOr("ORCHESTRATOR_STATE_FILE", "orchestrator-state.json")

	ctx := context.Background()
	orch, err := startOrchestratedEngine(ctx, database.ConfigFromEnv(), statePath)
	if err != nil {
		log.Fatalf("starting orchestrated engine: %v", err)
	}

	router := api.NewRouter(ingestion.New(), agent.New(), control.New(orch))

	log.Printf("backend listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("backend server failed: %v", err)
	}
}

// startOrchestratedEngine boots every microservice (each migrating its own database) before the orchestrator.
func startOrchestratedEngine(ctx context.Context, dbConfig database.Config, statePath string) (*orchestrator.Orchestrator, error) {
	reg := registry.New()

	customers, err := customer.Start(ctx, dbConfig, reg)
	if err != nil {
		return nil, err
	}
	products, err := inventory.Start(ctx, dbConfig, reg)
	if err != nil {
		return nil, err
	}
	invoices, err := invoicing.Start(ctx, dbConfig, reg)
	if err != nil {
		return nil, err
	}
	orders, err := sales.Start(ctx, dbConfig, invoices.OrderEvents())
	if err != nil {
		return nil, err
	}

	orch, err := orchestrator.New(orchestrator.Targets{
		Customers: customers.Commands(),
		Products:  products.Commands(),
		Orders:    orders.Commands(),
		Payments:  invoices.Payments(),
	}, reg, statePath)
	if err != nil {
		return nil, err
	}
	return orch, orch.ResumeIfRunning()
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
