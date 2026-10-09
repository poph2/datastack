package inventory

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Reservation outcomes, used as the "outcome" metric label and span attribute.
const (
	outcomeReserved        = "reserved"
	outcomeOutOfStock      = "out_of_stock"
	outcomeProductNotFound = "product_not_found"
)

// Restock sources, used as the "source" metric label.
const (
	restockSourceAuto   = "auto"
	restockSourceManual = "manual"
)

// Domain metrics. The shared HTTP metrics count requests. These count what
// happened to the stock, which is what the Grafana dashboards chart.
var (
	reservationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_reservations_total",
		Help: "Reservation attempts, by outcome.",
	}, []string{"outcome"})

	unitsReservedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "inventory_units_reserved_total",
		Help: "Units taken out of stock by successful reservations.",
	})

	unitsReleasedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "inventory_units_released_total",
		Help: "Units returned to stock by cancelled checkouts.",
	})

	unitsRestockedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_units_restocked_total",
		Help: "Units added by restocks, by source (auto or manual).",
	}, []string{"source"})
)
