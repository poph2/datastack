package inventory

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/chaos"
	"github.com/poph2/datastack/pkgs/httpserver"
)

// Drainer empties stock on demand. *Repository implements it.
type Drainer interface {
	DrainStock(ctx context.Context, products int) ([]string, error)
}

// ChaosActions serves the Inventory Service's chaos action: draining random
// products to zero, so a demo can trigger OUT_OF_STOCK failures at will.
type ChaosActions struct {
	drainer Drainer
	logger  *slog.Logger
}

// NewChaosActions returns the chaos actions backed by drainer.
func NewChaosActions(drainer Drainer, logger *slog.Logger) *ChaosActions {
	return &ChaosActions{drainer: drainer, logger: logger}
}

// RegisterRoutes adds POST /admin/chaos/drain-stock to admin.
func (a *ChaosActions) RegisterRoutes(admin gin.IRouter) {
	admin.POST(chaos.AdminPath+"/drain-stock", a.drainStock)
}

// drainRequest's upper bound is the size of the seed catalog.
type drainRequest struct {
	Products int `json:"products" binding:"required,min=1,max=100"`
}

type drainResponse struct {
	Drained    int      `json:"drained"`
	ProductIDs []string `json:"product_ids"`
}

// drainStock sets stock to zero on randomly chosen products that still have
// some. Auto-restocked products recover on the restocker's next run (unless
// restock_paused is set); scarce ones stay sold out.
func (a *ChaosActions) drainStock(c *gin.Context) {
	var req drainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	ctx := c.Request.Context()
	drained, err := a.drainer.DrainStock(ctx, req.Products)
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	trace.SpanFromContext(ctx).SetAttributes(
		attribute.Int("chaos.drain.requested", req.Products),
		attribute.StringSlice("chaos.drain.product_ids", drained),
	)
	a.logger.InfoContext(ctx, "chaos drained stock", "requested", req.Products, "drained", len(drained), "product_ids", drained)
	c.JSON(http.StatusOK, drainResponse{Drained: len(drained), ProductIDs: drained})
}
