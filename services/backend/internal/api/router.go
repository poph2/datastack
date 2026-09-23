// Package api exposes the backend's REST JSON endpoints for the Next.js UI.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/poph2/datastack/services/backend/internal/agent"
	"github.com/poph2/datastack/services/backend/internal/ingestion"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRouter wires up all HTTP routes for the backend service.
func NewRouter(ingestionSvc *ingestion.Service, agentSvc *agent.Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /api/ingestion/status", handleIngestionStatus(ingestionSvc))
	mux.HandleFunc("POST /api/agent/ask", handleAgentAsk(agentSvc))
	mux.Handle("GET /metrics", promhttp.Handler())

	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleIngestionStatus(svc *ingestion.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, svc.Statuses())
	}
}

func handleAgentAsk(svc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Question string `json:"question"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		answer, err := svc.Ask(req.Question)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, answer)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
