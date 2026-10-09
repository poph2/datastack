package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// Error codes returned by this service, in addition to the shared ones in
// httpserver. The Order Service records PAYMENT_DECLINED as an order's
// failure_reason.
const (
	codePaymentNotFound = "PAYMENT_NOT_FOUND"
	codePaymentDeclined = "PAYMENT_DECLINED"
	codeAlreadyPaid     = "ALREADY_PAID"
	codeNotRefundable   = "NOT_REFUNDABLE"
)

// maxAmount is the largest value DECIMAL(10,2) can hold. Larger amounts are
// rejected as bad input instead of failing in Postgres with a 500.
var maxAmount = decimal.RequireFromString("99999999.99")

// Store is the persistence the handler needs. *Repository implements it, and
// tests use an in-memory fake.
type Store interface {
	Create(ctx context.Context, p *Payment) error
	Get(ctx context.Context, id uint64) (Payment, error)
	Refund(ctx context.Context, id uint64) (Payment, error)
}

// Handler serves the Payment Service REST API.
type Handler struct {
	store   Store
	gateway *Gateway
	methods *MethodPicker
}

// NewHandler returns a Handler. gateway decides each charge's outcome, and
// methods assigns a payment method to charges made without one.
func NewHandler(store Store, gateway *Gateway, methods *MethodPicker) *Handler {
	return &Handler{store: store, gateway: gateway, methods: methods}
}

// RegisterRoutes adds the payment endpoints to api.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.POST("/payments/process", h.process)
	api.GET("/payments/:id", h.get)
	api.POST("/payments/:id/refund", h.refund)
}

type processRequest struct {
	OrderID uint64 `json:"order_id" binding:"required,min=1"`
	// decimal.Decimal accepts both "99.99" and 99.99. It is validated in
	// validateAmount, since binding tags can't express decimal rules.
	Amount decimal.Decimal `json:"amount"`
	Method Method          `json:"method" binding:"omitempty,oneof=CREDIT_CARD PAYPAL"`
}

type paymentURI struct {
	ID uint64 `uri:"id" binding:"required,min=1"`
}

type paymentResponse struct {
	PaymentID     uint64         `json:"payment_id"`
	OrderID       uint64         `json:"order_id"`
	Amount        string         `json:"amount"`
	Method        Method         `json:"method"`
	Status        Status         `json:"status"`
	DeclineReason *DeclineReason `json:"decline_reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// declinedResponse is the shared error body plus the recorded payment, so the
// caller sees the decline reason and the payment ID of the failed attempt.
type declinedResponse struct {
	httpserver.ErrorResponse
	Payment paymentResponse `json:"payment"`
}

// process charges an order. It is on the checkout critical path.
func (h *Handler) process(c *gin.Context) {
	var req processRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	if err := validateAmount(req.Amount); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	if req.Method == "" {
		req.Method = h.methods.Pick()
	}

	ctx := c.Request.Context()
	decision, err := h.gateway.Authorize(ctx)
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	payment := Payment{
		OrderID:       req.OrderID,
		Amount:        req.Amount,
		Method:        req.Method,
		Status:        decision.Status,
		DeclineReason: decision.DeclineReason,
	}
	err = h.store.Create(ctx, &payment)
	if errors.Is(err, ErrAlreadyPaid) {
		httpserver.RespondError(c, http.StatusConflict, codeAlreadyPaid, "this order already has a successful payment")
		return
	}
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	recordPayment(payment)
	annotateSpan(c, payment)

	// A decline is a business outcome, not a server error: the row is
	// recorded, and the span is annotated but not marked as failed.
	if payment.Status == StatusDeclined {
		c.JSON(http.StatusPaymentRequired, declinedResponse{
			ErrorResponse: httpserver.ErrorResponse{
				Error:   codePaymentDeclined,
				Message: fmt.Sprintf("payment declined: %s", *payment.DeclineReason),
			},
			Payment: toResponse(payment),
		})
		return
	}
	c.JSON(http.StatusOK, toResponse(payment))
}

func (h *Handler) get(c *gin.Context) {
	var uri paymentURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	payment, err := h.store.Get(c.Request.Context(), uri.ID)
	if !respondStoreError(c, err) {
		return
	}

	annotateSpan(c, payment)
	c.JSON(http.StatusOK, toResponse(payment))
}

// refund simulates a customer return: SUCCESS becomes REFUNDED.
func (h *Handler) refund(c *gin.Context) {
	var uri paymentURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	payment, err := h.store.Refund(c.Request.Context(), uri.ID)
	if !respondStoreError(c, err) {
		return
	}

	recordPayment(payment)
	annotateSpan(c, payment)
	c.JSON(http.StatusOK, toResponse(payment))
}

// validateAmount enforces what DECIMAL(10,2) and the CHECK constraint need:
// positive, at most two decimal places, and small enough to store.
func validateAmount(amount decimal.Decimal) error {
	switch {
	case !amount.IsPositive():
		return errors.New("amount is required and must be greater than 0")
	case !amount.Equal(amount.Round(moneyDecimals)):
		return fmt.Errorf("amount must have at most %d decimal places", moneyDecimals)
	case amount.GreaterThan(maxAmount):
		return fmt.Errorf("amount must not exceed %s", maxAmount.StringFixed(moneyDecimals))
	}
	return nil
}

// respondStoreError writes the response for a failed lookup or refund and
// reports whether the handler should continue.
func respondStoreError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrNotFound):
		httpserver.RespondError(c, http.StatusNotFound, codePaymentNotFound, "payment not found")
	case errors.Is(err, ErrNotRefundable):
		httpserver.RespondError(c, http.StatusConflict, codeNotRefundable, "only a SUCCESS payment can be refunded")
	default:
		httpserver.RespondInternalError(c, err)
	}
	return false
}

// annotateSpan records the payment on the request span, so a checkout trace
// shows what was charged and how it ended.
func annotateSpan(c *gin.Context, p Payment) {
	attributes := []attribute.KeyValue{
		attribute.Int64("payment.id", int64(p.ID)),
		attribute.Int64("order.id", int64(p.OrderID)),
		attribute.Float64("payment.amount", p.Amount.InexactFloat64()),
		attribute.String("payment.method", string(p.Method)),
		attribute.String("payment.status", string(p.Status)),
	}
	if p.DeclineReason != nil {
		attributes = append(attributes, attribute.String("payment.decline_reason", string(*p.DeclineReason)))
	}
	trace.SpanFromContext(c.Request.Context()).SetAttributes(attributes...)
}

func toResponse(p Payment) paymentResponse {
	return paymentResponse{
		PaymentID:     p.ID,
		OrderID:       p.OrderID,
		Amount:        p.Amount.StringFixed(moneyDecimals),
		Method:        p.Method,
		Status:        p.Status,
		DeclineReason: p.DeclineReason,
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
	}
}
