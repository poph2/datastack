package order

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Domain metrics: the executive heartbeat. The shared HTTP client metrics
// already show each downstream call's latency and status.
var (
	checkoutsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orders_checkouts_total",
		Help: "Completed checkouts, by final status (SHIPPED, PAID, FAILED) and failure reason.",
	}, []string{"status", "reason"})

	revenueTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "orders_revenue_total",
		Help: "Total amount of successfully paid orders, in dollars.",
	})

	reconciledTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orders_reconciled_total",
		Help: "Orders stuck in PAID that the reconciler retried, by outcome (shipped, failed).",
	}, []string{"outcome"})
)
