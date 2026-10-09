package payment

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Domain metrics. The shared HTTP metrics count requests. These count what
// happened to the money, which is what the Grafana dashboards chart.
var (
	paymentsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "payments_total",
		Help: "Payments recorded, by resulting status and method. A refund counts once more as REFUNDED.",
	}, []string{"status", "method"})

	paymentDeclinesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "payment_declines_total",
		Help: "Declined charges, by decline reason.",
	}, []string{"reason"})

	// Dollars as a float are fine for a dashboard rate. The exact figures
	// are the DECIMAL amounts in the payments table.
	paymentsAmountTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "payments_amount_total",
		Help: "Sum of payment amounts in dollars, by resulting status.",
	}, []string{"status"})
)

// recordPayment counts a payment that has just been stored with its current
// status, either by a charge or by a refund.
func recordPayment(p Payment) {
	paymentsTotal.WithLabelValues(string(p.Status), string(p.Method)).Inc()
	paymentsAmountTotal.WithLabelValues(string(p.Status)).Add(p.Amount.InexactFloat64())
	if p.DeclineReason != nil {
		paymentDeclinesTotal.WithLabelValues(string(*p.DeclineReason)).Inc()
	}
}
