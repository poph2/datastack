package chaos

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// knobValue lets Grafana show when chaos is active. The service label comes
// from the scrape target, so only the knob is a label here.
var knobValue = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "chaos_knob_value",
	Help: "Current value of each runtime chaos knob (see /admin/chaos).",
}, []string{"knob"})
