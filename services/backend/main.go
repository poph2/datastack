// Command backend runs the DataStack Go backend API: ingestion control and the data agent.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/poph2/datastack/services/backend/internal/agent"
	"github.com/poph2/datastack/services/backend/internal/api"
	"github.com/poph2/datastack/services/backend/internal/ingestion"
)

func main() {
	addr := os.Getenv("BACKEND_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ingestionSvc := ingestion.New()
	agentSvc := agent.New()

	router := api.NewRouter(ingestionSvc, agentSvc)

	log.Printf("backend listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("backend server failed: %v", err)
	}
}
