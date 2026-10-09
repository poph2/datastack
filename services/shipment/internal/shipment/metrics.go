package shipment

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// timeToShipBuckets span the configured packing delays (2s to 30s by
// default) plus the queueing a k6 burst adds on top.
var timeToShipBuckets = []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300}

// Domain metrics. The shared HTTP metrics count requests. These count what
// happened to the boxes, which is what the fulfillment dashboards chart.
var (
	shipmentsCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shipments_created_total",
		Help: "Shipments created, by carrier. Idempotent repeats of an existing order are not counted.",
	}, []string{"carrier"})

	shipmentsShippedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shipments_shipped_total",
		Help: "Shipments moved to SHIPPED by the packer, by carrier.",
	}, []string{"carrier"})

	timeToShipSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "shipment_time_to_ship_seconds",
		Help:    "Time from shipment creation to SHIPPED (shipped_at - created_at, both from the database clock).",
		Buckets: timeToShipBuckets,
	})

	shipmentsPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shipments_pending",
		Help: "Shipments still PENDING (the packing backlog), as of the last packer run.",
	})
)
