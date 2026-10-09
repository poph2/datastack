package marketing

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Domain metrics. The shared HTTP metrics count requests. These count clicks,
// conversions and attributed revenue, which is what the marketing dashboards
// chart. utm_source is a safe label: an attribution can only be logged for a
// seeded campaign, so its values are a small fixed set.
var (
	attributionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "marketing_attributions_total",
		Help: "Touchpoints (ad clicks or visits) logged, by utm_source.",
	}, []string{"utm_source"})

	conversionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "marketing_conversions_total",
		Help: "Attributions converted into a paid order, by utm_source. Idempotent re-sends are not counted.",
	}, []string{"utm_source"})

	conversionRevenueTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "marketing_conversion_revenue_total",
		Help: "Order amount attributed to converted touchpoints, in dollars, by utm_source.",
	}, []string{"utm_source"})
)

// recordConversion counts a first-time conversion. Prometheus counters are
// floats, so the revenue here is approximate; the exact figure is the
// DECIMAL in Postgres and StarRocks.
func recordConversion(a Attribution) {
	conversionsTotal.WithLabelValues(a.UTMSource).Inc()
	if a.OrderAmount != nil {
		conversionRevenueTotal.WithLabelValues(a.UTMSource).Add(a.OrderAmount.InexactFloat64())
	}
}
