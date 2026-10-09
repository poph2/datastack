package httpclient

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Client-side view of service-to-service calls. A peer's own server metrics
// can't show time lost to the network or to timeouts; these can.
var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_client_requests_total",
		Help: "Outgoing HTTP requests to peer services, by peer, method and status (\"error\" for transport failures and timeouts).",
	}, []string{"peer", "method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_client_request_duration_seconds",
		Help:    "Outgoing HTTP request latency to peer services, by peer and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"peer", "method"})
)

func observe(peer, method, status string, start time.Time) {
	requestsTotal.WithLabelValues(peer, method, status).Inc()
	requestDuration.WithLabelValues(peer, method).Observe(time.Since(start).Seconds())
}
