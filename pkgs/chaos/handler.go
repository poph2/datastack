package chaos

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// AdminPath is where the knobs are served. Service-specific chaos actions,
// such as the Inventory Service's drain-stock, live below it.
const AdminPath = "/admin/chaos"

// codeRequestCancelled answers a request whose caller gave up while it was
// held by the injected latency. The caller is gone, so it is only logged.
const codeRequestCancelled = "REQUEST_CANCELLED"

// statusResponse is the body of every /admin/chaos response.
type statusResponse struct {
	Service string  `json:"service"`
	Knobs   []State `json:"knobs"`
}

// Routes returns a RegisterRoutes function for httpserver.RouterConfig. It
// registers the /admin/chaos endpoints and any extra admin routes, then the
// service's API routes behind the latency middleware. Admin routes are
// traced, logged and metered like API routes, but never delayed, so the knobs
// can always be turned back.
func (s *Store) Routes(api func(gin.IRouter), admin ...func(gin.IRouter)) func(gin.IRouter) {
	return func(r gin.IRouter) {
		r.GET(AdminPath, s.get)
		r.PUT(AdminPath, s.put)
		r.DELETE(AdminPath, s.reset)
		for _, register := range admin {
			register(r)
		}
		api(r.Group("/", s.Middleware()))
	}
}

// Middleware delays each request by the current latency knob. The delay is
// recorded on the request span, so it is visible in Tempo, and it ends early
// if the caller gives up.
func (s *Store) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		delay := s.Latency()
		if delay == 0 {
			return
		}

		ctx := c.Request.Context()
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.Int64("chaos.latency_ms", delay.Milliseconds()))
		span.AddEvent("chaos.latency.start")

		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			httpserver.RespondError(c, http.StatusServiceUnavailable, codeRequestCancelled,
				"request cancelled during injected chaos latency")
		case <-timer.C:
			span.AddEvent("chaos.latency.end")
		}
	}
}

func (s *Store) get(c *gin.Context) {
	s.respond(c)
}

// put applies a partial update such as {"latency_ms": 500}.
func (s *Store) put(c *gin.Context) {
	var changes map[string]float64
	if err := c.ShouldBindJSON(&changes); err != nil {
		httpserver.RespondInvalidRequest(c, fmt.Errorf("body must be a JSON object of knob names to numbers: %w", err))
		return
	}

	// Apply can only fail validation.
	if err := s.Apply(c.Request.Context(), changes); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	s.respond(c)
}

func (s *Store) reset(c *gin.Context) {
	s.Reset(c.Request.Context())
	s.respond(c)
}

func (s *Store) respond(c *gin.Context) {
	c.JSON(http.StatusOK, statusResponse{Service: s.service, Knobs: s.States()})
}
