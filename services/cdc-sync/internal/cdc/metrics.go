package cdc

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Batch outcomes, used as the "outcome" metric label.
const (
	outcomeLoaded        = "loaded"
	outcomeAlreadyLoaded = "already_loaded"
	outcomeFailed        = "failed"
)

// Sync metrics. The "table" label is the "<database>.<table>" source name.
var (
	rowsSyncedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cdc_rows_synced_total",
		Help: "Rows delivered to StarRocks, by source table.",
	}, []string{"table"})

	batchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cdc_batches_total",
		Help: "Batches attempted, by source table and outcome (loaded, already_loaded, failed).",
	}, []string{"table", "outcome"})

	batchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cdc_batch_duration_seconds",
		Help:    "Time to read and load one non-empty batch, by source table.",
		Buckets: prometheus.DefBuckets,
	}, []string{"table"})

	lagSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cdc_lag_seconds",
		Help: "Newest source updated_at minus the sync cursor, by source table (0 when caught up).",
	}, []string{"table"})
)
