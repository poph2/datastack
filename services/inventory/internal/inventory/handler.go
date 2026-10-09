package inventory

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// Error codes returned by this service, in addition to the shared ones in
// httpserver. The Order Service records OUT_OF_STOCK as an order's
// failure_reason.
const (
	codeProductNotFound = "PRODUCT_NOT_FOUND"
	codeOutOfStock      = "OUT_OF_STOCK"
)

// Store is the persistence the handler needs. *Repository implements it, and
// tests use an in-memory fake.
type Store interface {
	List(ctx context.Context) ([]Product, error)
	Get(ctx context.Context, productID string) (Product, error)
	Reserve(ctx context.Context, productID string, quantity int) (Product, error)
	AddStock(ctx context.Context, productID string, quantity int) (Product, error)
}

// Handler serves the Inventory Service REST API.
type Handler struct {
	store Store
}

// NewHandler returns a Handler backed by store.
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes adds the inventory endpoints to api.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.GET("/inventory", h.list)
	api.GET("/inventory/:product_id", h.get)
	api.POST("/inventory/reserve", h.reserve)
	api.POST("/inventory/release", h.release)
	api.POST("/inventory/restock", h.restock)
}

// stockChangeRequest is the body of reserve, release and restock.
type stockChangeRequest struct {
	ProductID string `json:"product_id" binding:"required,max=50"`
	Quantity  int    `json:"quantity" binding:"required,min=1,max=10000"`
}

type productURI struct {
	ProductID string `uri:"product_id" binding:"required,max=50"`
}

type productResponse struct {
	ProductID   string    `json:"product_id"`
	Category    string    `json:"category"`
	StockLevel  int       `json:"stock_level"`
	Price       string    `json:"price"`
	AutoRestock bool      `json:"auto_restock"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type reserveResponse struct {
	ProductID      string `json:"product_id"`
	Quantity       int    `json:"quantity"`
	UnitPrice      string `json:"unit_price"`
	Category       string `json:"category"`
	RemainingStock int    `json:"remaining_stock"`
}

type stockChangeResponse struct {
	ProductID      string `json:"product_id"`
	Quantity       int    `json:"quantity"`
	RemainingStock int    `json:"remaining_stock"`
}

func (h *Handler) list(c *gin.Context) {
	products, err := h.store.List(c.Request.Context())
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	response := make([]productResponse, 0, len(products))
	for _, p := range products {
		response = append(response, toProductResponse(p))
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) get(c *gin.Context) {
	var uri productURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	product, err := h.store.Get(c.Request.Context(), uri.ProductID)
	if !respondStoreError(c, err) {
		return
	}
	c.JSON(http.StatusOK, toProductResponse(product))
}

// reserve is on the checkout critical path. It takes stock out, or explains
// why it can't, and returns the unit price the Order Service charges.
func (h *Handler) reserve(c *gin.Context) {
	var req stockChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	product, err := h.store.Reserve(c.Request.Context(), req.ProductID, req.Quantity)
	recordReservation(c, req, err)
	if !respondStoreError(c, err) {
		return
	}

	annotateSpan(c, req, product.StockLevel)
	c.JSON(http.StatusOK, reserveResponse{
		ProductID:      product.ProductID,
		Quantity:       req.Quantity,
		UnitPrice:      product.Price.StringFixed(moneyDecimals),
		Category:       product.Category,
		RemainingStock: product.StockLevel,
	})
}

// release is the Order Service's compensation when payment fails after a
// successful reservation.
func (h *Handler) release(c *gin.Context) {
	h.addStock(c, func(quantity int) { unitsReleasedTotal.Add(float64(quantity)) })
}

// restock lets k6 trigger replenishment bursts on top of the background loop.
func (h *Handler) restock(c *gin.Context) {
	h.addStock(c, func(quantity int) {
		unitsRestockedTotal.WithLabelValues(restockSourceManual).Add(float64(quantity))
	})
}

func (h *Handler) addStock(c *gin.Context, record func(quantity int)) {
	var req stockChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	product, err := h.store.AddStock(c.Request.Context(), req.ProductID, req.Quantity)
	if !respondStoreError(c, err) {
		return
	}

	record(req.Quantity)
	annotateSpan(c, req, product.StockLevel)
	c.JSON(http.StatusOK, stockChangeResponse{
		ProductID:      product.ProductID,
		Quantity:       req.Quantity,
		RemainingStock: product.StockLevel,
	})
}

// respondStoreError writes the response for a failed store call and reports
// whether the handler should continue.
func respondStoreError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrNotFound):
		httpserver.RespondError(c, http.StatusNotFound, codeProductNotFound, "product not found")
	case errors.Is(err, ErrOutOfStock):
		httpserver.RespondError(c, http.StatusConflict, codeOutOfStock, "not enough stock to reserve the requested quantity")
	default:
		httpserver.RespondInternalError(c, err)
	}
	return false
}

// recordReservation counts the reservation outcome in metrics and on the
// span. Unexpected errors are left to the HTTP 5xx metrics.
func recordReservation(c *gin.Context, req stockChangeRequest, err error) {
	var outcome string
	switch {
	case err == nil:
		outcome = outcomeReserved
		unitsReservedTotal.Add(float64(req.Quantity))
	case errors.Is(err, ErrOutOfStock):
		outcome = outcomeOutOfStock
	case errors.Is(err, ErrNotFound):
		outcome = outcomeProductNotFound
	default:
		return
	}

	reservationsTotal.WithLabelValues(outcome).Inc()
	trace.SpanFromContext(c.Request.Context()).SetAttributes(attribute.String("inventory.outcome", outcome))
}

func annotateSpan(c *gin.Context, req stockChangeRequest, remainingStock int) {
	trace.SpanFromContext(c.Request.Context()).SetAttributes(
		attribute.String("product.id", req.ProductID),
		attribute.Int("inventory.quantity", req.Quantity),
		attribute.Int("inventory.remaining_stock", remainingStock),
	)
}

func toProductResponse(p Product) productResponse {
	return productResponse{
		ProductID:   p.ProductID,
		Category:    p.Category,
		StockLevel:  p.StockLevel,
		Price:       p.Price.StringFixed(moneyDecimals),
		AutoRestock: p.AutoRestock,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}
