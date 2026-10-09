package analyzer

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
)

// tracer creates the analyzer's own spans: one per pipeline step.
var tracer = otel.Tracer("github.com/poph2/datastack/services/analyzer")

// Outcome label values.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

var (
	asksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "analyzer_asks_total",
		Help: "Questions answered, by outcome (success, unsafe_sql, query_failed, no_sql, llm_unavailable) and model.",
	}, []string{"outcome", "model"})

	askAttempts = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "analyzer_ask_attempts",
		Help:    "Generation attempts per question (1 means no repair was needed), by outcome.",
		Buckets: []float64{1, 2, 3, 4, 5},
	}, []string{"outcome"})

	llmDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "analyzer_llm_request_duration_seconds",
		Help:    "Time for one LLM generation, by provider and model.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 90, 120, 180},
	}, []string{"provider", "model"})

	schemaRefreshesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "analyzer_schema_refreshes_total",
		Help: "Schema refreshes (read-only grant plus information_schema read), by outcome.",
	}, []string{"outcome"})
)
