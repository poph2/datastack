package customer

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// Error codes returned by this service, in addition to the shared ones in
// httpserver.
const (
	codeCustomerNotFound = "CUSTOMER_NOT_FOUND"
	codeEmailExists      = "EMAIL_EXISTS"
)

// Store is the persistence the handler needs. *Repository implements it, and
// tests use an in-memory fake.
type Store interface {
	Create(ctx context.Context, c *Customer) error
	Get(ctx context.Context, id uint64) (Customer, error)
	Update(ctx context.Context, id uint64, changes Changes) (Customer, error)
	Delete(ctx context.Context, id uint64) error
}

// Handler serves the Customer Service REST API.
type Handler struct {
	store Store
	tiers *TierPicker
}

// NewHandler returns a Handler. tiers assigns a loyalty tier to customers
// created without one.
func NewHandler(store Store, tiers *TierPicker) *Handler {
	return &Handler{store: store, tiers: tiers}
}

// RegisterRoutes adds the customer endpoints to api.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.POST("/customers", h.create)
	api.GET("/customers/:id", h.get)
	api.PATCH("/customers/:id", h.update)
	api.DELETE("/customers/:id", h.delete)
}

type createCustomerRequest struct {
	Email       string      `json:"email" binding:"required,email,max=255"`
	Country     string      `json:"country" binding:"required,iso3166_1_alpha2"`
	LoyaltyTier LoyaltyTier `json:"loyalty_tier" binding:"omitempty,oneof=BRONZE SILVER GOLD"`
}

type updateCustomerRequest struct {
	LoyaltyTier *LoyaltyTier `json:"loyalty_tier" binding:"omitempty,oneof=BRONZE SILVER GOLD"`
	Status      *Status      `json:"status" binding:"omitempty,oneof=ACTIVE SUSPENDED PENDING_DELETION"`
}

type customerURI struct {
	ID uint64 `uri:"id" binding:"required,min=1"`
}

type customerResponse struct {
	ID          uint64      `json:"id"`
	Email       string      `json:"email"`
	Country     string      `json:"country"`
	LoyaltyTier LoyaltyTier `json:"loyalty_tier"`
	Status      Status      `json:"status"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func (h *Handler) create(c *gin.Context) {
	var req createCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	customer := Customer{
		Email:       strings.ToLower(req.Email),
		Country:     req.Country,
		LoyaltyTier: req.LoyaltyTier,
		Status:      StatusActive,
	}
	if customer.LoyaltyTier == "" {
		customer.LoyaltyTier = h.tiers.Pick()
	}

	err := h.store.Create(c.Request.Context(), &customer)
	if errors.Is(err, ErrEmailExists) {
		httpserver.RespondError(c, http.StatusConflict, codeEmailExists, "a customer with this email already exists")
		return
	}
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	annotateSpan(c, customer)
	c.JSON(http.StatusCreated, toResponse(customer))
}

func (h *Handler) get(c *gin.Context) {
	var uri customerURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	customer, err := h.store.Get(c.Request.Context(), uri.ID)
	if !h.handleLookupError(c, err) {
		return
	}

	annotateSpan(c, customer)
	c.JSON(http.StatusOK, toResponse(customer))
}

func (h *Handler) update(c *gin.Context) {
	var uri customerURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	var req updateCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	if req.LoyaltyTier == nil && req.Status == nil {
		httpserver.RespondError(c, http.StatusBadRequest, httpserver.CodeInvalidRequest,
			"provide at least one of loyalty_tier or status")
		return
	}

	customer, err := h.store.Update(c.Request.Context(), uri.ID, Changes(req))
	if !h.handleLookupError(c, err) {
		return
	}

	annotateSpan(c, customer)
	c.JSON(http.StatusOK, toResponse(customer))
}

func (h *Handler) delete(c *gin.Context) {
	var uri customerURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	err := h.store.Delete(c.Request.Context(), uri.ID)
	if !h.handleLookupError(c, err) {
		return
	}
	c.Status(http.StatusNoContent)
}

// handleLookupError writes the response for a failed lookup by ID and
// reports whether the handler should continue.
func (h *Handler) handleLookupError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrNotFound):
		httpserver.RespondError(c, http.StatusNotFound, codeCustomerNotFound, "customer not found")
	default:
		httpserver.RespondInternalError(c, err)
	}
	return false
}

// annotateSpan records the customer on the request span, so traces from the
// Order Service's checkout show which customer was validated and their status.
func annotateSpan(c *gin.Context, customer Customer) {
	trace.SpanFromContext(c.Request.Context()).SetAttributes(
		attribute.Int64("customer.id", int64(customer.ID)),
		attribute.String("customer.loyalty_tier", string(customer.LoyaltyTier)),
		attribute.String("customer.status", string(customer.Status)),
	)
}

func toResponse(c Customer) customerResponse {
	return customerResponse{
		ID:          c.ID,
		Email:       c.Email,
		Country:     c.Country,
		LoyaltyTier: c.LoyaltyTier,
		Status:      c.Status,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}
