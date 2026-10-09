package order

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// codeOrderNotFound is returned by GET /orders/:id for an unknown order.
const codeOrderNotFound = "ORDER_NOT_FOUND"

// Checkouts runs checkouts. *Checkout implements it.
type Checkouts interface {
	Run(ctx context.Context, req CheckoutRequest) (CheckoutResult, error)
}

// Handler serves the Order Service REST API.
type Handler struct {
	checkouts Checkouts
	store     Store
}

// NewHandler returns a Handler.
func NewHandler(checkouts Checkouts, store Store) *Handler {
	return &Handler{checkouts: checkouts, store: store}
}

// RegisterRoutes adds the order endpoints to api.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.POST("/orders/checkout", h.checkout)
	api.GET("/orders/:id", h.get)
}

type checkoutRequest struct {
	UserID        uint64  `json:"user_id" binding:"required,min=1"`
	ProductID     string  `json:"product_id" binding:"required,max=50"`
	AttributionID *uint64 `json:"attribution_id" binding:"omitempty,min=1"`
	Quantity      int     `json:"quantity" binding:"omitempty,min=1,max=100"`
}

type orderURI struct {
	ID uint64 `uri:"id" binding:"required,min=1"`
}

type orderResponse struct {
	OrderID        uint64         `json:"order_id"`
	UserID         uint64         `json:"user_id"`
	AttributionID  *uint64        `json:"attribution_id"`
	ProductID      string         `json:"product_id"`
	Quantity       int            `json:"quantity"`
	UnitPrice      *string        `json:"unit_price"`
	TotalAmount    *string        `json:"total_amount"`
	Status         Status         `json:"status"`
	FailureReason  *FailureReason `json:"failure_reason"`
	TrackingNumber string         `json:"tracking_number,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// checkoutFailure is the error body of a failed checkout: the shared error
// shape plus the FAILED order, so the caller still learns its ID.
type checkoutFailure struct {
	httpserver.ErrorResponse
	Order orderResponse `json:"order"`
}

// checkout runs one purchase. A shipped (or paid, awaiting shipment) order is
// 201. A business failure is 422 with the reason as the error code, and only
// an unreachable dependency (UPSTREAM_ERROR) is 502, so k6's 5xx rate means
// "the system is broken", not "the card was declined".
func (h *Handler) checkout(c *gin.Context) {
	var req checkoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	if req.Quantity == 0 {
		req.Quantity = 1
	}

	result, err := h.checkouts.Run(c.Request.Context(), CheckoutRequest{
		UserID:        req.UserID,
		ProductID:     req.ProductID,
		Quantity:      req.Quantity,
		AttributionID: req.AttributionID,
	})
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	response := toResponse(result.Order)
	response.TrackingNumber = result.TrackingNumber

	if result.Order.Status != StatusFailed {
		c.JSON(http.StatusCreated, response)
		return
	}

	reason := *result.Order.FailureReason
	trace.SpanFromContext(c.Request.Context()).SetStatus(codes.Error, string(reason))

	status := http.StatusUnprocessableEntity
	if reason == ReasonUpstreamError {
		status = http.StatusBadGateway
	}
	c.AbortWithStatusJSON(status, checkoutFailure{
		ErrorResponse: httpserver.ErrorResponse{Error: string(reason), Message: failureMessages[reason]},
		Order:         response,
	})
}

var failureMessages = map[FailureReason]string{
	ReasonCustomerInvalid:   "customer does not exist",
	ReasonCustomerSuspended: "customer account is not active",
	ReasonOutOfStock:        "not enough stock for this product",
	ReasonProductNotFound:   "product does not exist",
	ReasonPaymentDeclined:   "payment was declined",
	ReasonUpstreamError:     "a dependency was unavailable; the order was not completed",
}

func (h *Handler) get(c *gin.Context) {
	var uri orderURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	order, err := h.store.Get(c.Request.Context(), uri.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		httpserver.RespondError(c, http.StatusNotFound, codeOrderNotFound, "order not found")
	case err != nil:
		httpserver.RespondInternalError(c, err)
	default:
		c.JSON(http.StatusOK, toResponse(order))
	}
}

func toResponse(o Order) orderResponse {
	return orderResponse{
		OrderID:       o.ID,
		UserID:        o.UserID,
		AttributionID: o.AttributionID,
		ProductID:     o.ProductID,
		Quantity:      o.Quantity,
		UnitPrice:     formatMoney(o.UnitPrice),
		TotalAmount:   formatMoney(o.TotalAmount),
		Status:        o.Status,
		FailureReason: o.FailureReason,
		CreatedAt:     o.CreatedAt,
		UpdatedAt:     o.UpdatedAt,
	}
}

// formatMoney renders an optional amount as a fixed two-decimal string.
func formatMoney(amount *decimal.Decimal) *string {
	if amount == nil {
		return nil
	}
	s := amount.StringFixed(moneyDecimals)
	return &s
}
