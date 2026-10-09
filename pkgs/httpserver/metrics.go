package httpserver

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// unmatchedRoute labels requests that hit no route. Using the raw path
// instead would let any client create unlimited label values.
const unmatchedRoute = "unmatched"

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_server_requests_total",
		Help: "HTTP requests handled, by route, method and status code.",
	}, []string{"route", "method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_request_duration_seconds",
		Help:    "HTTP request latency, by route and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})
)

func requestMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = unmatchedRoute
		}
		method := c.Request.Method

		requestsTotal.WithLabelValues(route, method, strconv.Itoa(c.Writer.Status())).Inc()
		requestDuration.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
	}
}
