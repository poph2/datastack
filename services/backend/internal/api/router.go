// Package api exposes the backend's REST JSON endpoints for the Next.js UI.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/poph2/datastack/services/backend/internal/agent"
	"github.com/poph2/datastack/services/backend/internal/control"
	"github.com/poph2/datastack/services/backend/internal/ingestion"
	"github.com/poph2/datastack/services/backend/internal/orchestrator"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRouter wires up all HTTP routes for the backend service.
func NewRouter(ingestionSvc *ingestion.Service, agentSvc *agent.Service, controlSvc *control.Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /api/ingestion/status", handleIngestionStatus(ingestionSvc))
	mux.HandleFunc("POST /api/agent/ask", handleAgentAsk(agentSvc))
	mux.HandleFunc("GET /api/control/status", handleControlStatus(controlSvc))
	mux.HandleFunc("POST /api/control/orchestrator/start", handleControlAction(controlSvc.StartOrchestrator))
	mux.HandleFunc("POST /api/control/orchestrator/stop", handleControlAction(controlSvc.StopOrchestrator))
	mux.HandleFunc("POST /api/control/ingestion/start", handleControlAction(controlSvc.StartIngestion))
	mux.HandleFunc("POST /api/control/ingestion/stop", handleControlAction(controlSvc.StopIngestion))
	mux.Handle("GET /metrics", promhttp.Handler())

	return withCORS(mux)
}

// withCORS allows the browser-hosted UI (a different origin/port) to call the API directly.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleIngestionStatus(svc *ingestion.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, svc.Statuses())
	}
}

func handleControlStatus(svc *control.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, svc.Status())
	}
}

func handleControlAction(action func() (control.Status, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := action()
		switch {
		case errors.Is(err, orchestrator.ErrCaughtUp):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "status": status})
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, status)
		}
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
