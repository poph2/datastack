package marketing

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// Error codes returned by this service, in addition to the shared ones in
// httpserver.
const (
	codeCampaignNotFound    = "CAMPAIGN_NOT_FOUND"
	codeAttributionNotFound = "ATTRIBUTION_NOT_FOUND"
	codeAlreadyConverted    = "ALREADY_CONVERTED"
)

// maxOrderAmount is the largest value a DECIMAL(10,2) column holds. Larger
// amounts are rejected as bad input instead of failing in Postgres as a 500.
var maxOrderAmount = decimal.RequireFromString("99999999.99")

// Store is the persistence the handler needs. *Repository implements it, and
// tests use an in-memory fake.
type Store interface {
	ListCampaigns(ctx context.Context) ([]Campaign, error)
	CreateAttribution(ctx context.Context, a *Attribution) error
	GetAttribution(ctx context.Context, id uint64) (Attribution, error)
	Convert(ctx context.Context, id uint64, conversion Conversion) (ConvertResult, error)
}

// Handler serves the Marketing Service REST API.
type Handler struct {
	store Store
}

// NewHandler returns a Handler backed by store.
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes adds the marketing endpoints to api.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.GET("/campaigns", h.listCampaigns)
	api.POST("/attributions", h.createAttribution)
	api.GET("/attributions/:id", h.getAttribution)
	api.PUT("/attributions/:id/order", h.convert)
}

type createAttributionRequest struct {
	UserID      uint64 `json:"user_id" binding:"required,min=1"`
	UTMSource   string `json:"utm_source" binding:"required,max=100"`
	UTMCampaign string `json:"utm_campaign" binding:"required,max=100"`
}

// convertRequest's amount accepts a JSON string ("150.00") or number (150).
type convertRequest struct {
	OrderID     uint64           `json:"order_id" binding:"required,min=1"`
	OrderAmount *decimal.Decimal `json:"order_amount" binding:"required"`
}

type attributionURI struct {
	ID uint64 `uri:"id" binding:"required,min=1"`
}

type campaignResponse struct {
	CampaignID  uint64    `json:"campaign_id"`
	UTMSource   string    `json:"utm_source"`
	UTMCampaign string    `json:"utm_campaign"`
	AdSpend     string    `json:"ad_spend"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type createdAttributionResponse struct {
	AttributionID uint64    `json:"attribution_id"`
	UserID        uint64    `json:"user_id"`
	UTMSource     string    `json:"utm_source"`
	UTMCampaign   string    `json:"utm_campaign"`
	CreatedAt     time.Time `json:"created_at"`
}

// attributionResponse renders the conversion fields as null until the
// attribution converts.
type attributionResponse struct {
	AttributionID uint64     `json:"attribution_id"`
	UserID        uint64     `json:"user_id"`
	UTMSource     string     `json:"utm_source"`
	UTMCampaign   string     `json:"utm_campaign"`
	OrderID       *uint64    `json:"order_id"`
	OrderAmount   *string    `json:"order_amount"`
	ConvertedAt   *time.Time `json:"converted_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (h *Handler) listCampaigns(c *gin.Context) {
	campaigns, err := h.store.ListCampaigns(c.Request.Context())
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	response := make([]campaignResponse, 0, len(campaigns))
	for _, campaign := range campaigns {
		response = append(response, toCampaignResponse(campaign))
	}
	c.JSON(http.StatusOK, response)
}

// createAttribution logs a touchpoint. k6 calls it at the start of each user
// journey and carries the returned ID into checkout.
func (h *Handler) createAttribution(c *gin.Context) {
	var req createAttributionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	attribution := Attribution{UserID: req.UserID, UTMSource: req.UTMSource, UTMCampaign: req.UTMCampaign}
	if !respondStoreError(c, h.store.CreateAttribution(c.Request.Context(), &attribution)) {
		return
	}

	attributionsTotal.WithLabelValues(attribution.UTMSource).Inc()
	annotateSpan(c, attribution)
	c.JSON(http.StatusCreated, createdAttributionResponse{
		AttributionID: attribution.ID,
		UserID:        attribution.UserID,
		UTMSource:     attribution.UTMSource,
		UTMCampaign:   attribution.UTMCampaign,
		CreatedAt:     attribution.CreatedAt,
	})
}

func (h *Handler) getAttribution(c *gin.Context) {
	var uri attributionURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	attribution, err := h.store.GetAttribution(c.Request.Context(), uri.ID)
	if !respondStoreError(c, err) {
		return
	}

	annotateSpan(c, attribution)
	c.JSON(http.StatusOK, toAttributionResponse(attribution))
}

// convert is the conversion handshake. The Order Service calls it,
// best-effort, after a checkout is paid, and may retry it safely.
func (h *Handler) convert(c *gin.Context) {
	var uri attributionURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	var req convertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	if message := validateOrderAmount(*req.OrderAmount); message != "" {
		httpserver.RespondError(c, http.StatusBadRequest, httpserver.CodeInvalidRequest, message)
		return
	}

	result, err := h.store.Convert(c.Request.Context(), uri.ID, Conversion{OrderID: req.OrderID, OrderAmount: *req.OrderAmount})
	if !respondStoreError(c, err) {
		return
	}

	if result.FirstConversion {
		recordConversion(result.Attribution)
	}
	annotateSpan(c, result.Attribution)
	c.JSON(http.StatusOK, toAttributionResponse(result.Attribution))
}

// validateOrderAmount returns why amount can't be stored as a DECIMAL(10,2)
// order amount, or "" if it can.
func validateOrderAmount(amount decimal.Decimal) string {
	switch {
	case !amount.IsPositive():
		return "order_amount must be greater than zero"
	case !amount.Equal(amount.Round(moneyDecimals)):
		return "order_amount must have at most two decimal places"
	case amount.GreaterThan(maxOrderAmount):
		return "order_amount must be at most " + maxOrderAmount.StringFixed(moneyDecimals)
	}
	return ""
}

// respondStoreError writes the response for a failed store call and reports
// whether the handler should continue.
func respondStoreError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrCampaignNotFound):
		httpserver.RespondError(c, http.StatusNotFound, codeCampaignNotFound, "no campaign has this utm_source and utm_campaign")
	case errors.Is(err, ErrAttributionNotFound):
		httpserver.RespondError(c, http.StatusNotFound, codeAttributionNotFound, "attribution not found")
	case errors.Is(err, ErrAlreadyConverted):
		httpserver.RespondError(c, http.StatusConflict, codeAlreadyConverted, "attribution is already converted to a different order")
	default:
		httpserver.RespondInternalError(c, err)
	}
	return false
}

// annotateSpan records the attribution on the request span, so a checkout
// trace shows which campaign the order was credited to.
func annotateSpan(c *gin.Context, a Attribution) {
	span := trace.SpanFromContext(c.Request.Context())
	span.SetAttributes(
		attribute.Int64("attribution.id", int64(a.ID)),
		attribute.String("attribution.utm_source", a.UTMSource),
		attribute.String("attribution.utm_campaign", a.UTMCampaign),
		attribute.Int64("user.id", int64(a.UserID)),
	)
	if a.OrderID != nil {
		span.SetAttributes(attribute.Int64("order.id", int64(*a.OrderID)))
	}
}

func toCampaignResponse(c Campaign) campaignResponse {
	return campaignResponse{
		CampaignID:  c.ID,
		UTMSource:   c.UTMSource,
		UTMCampaign: c.UTMCampaign,
		AdSpend:     c.AdSpend.StringFixed(moneyDecimals),
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func toAttributionResponse(a Attribution) attributionResponse {
	response := attributionResponse{
		AttributionID: a.ID,
		UserID:        a.UserID,
		UTMSource:     a.UTMSource,
		UTMCampaign:   a.UTMCampaign,
		OrderID:       a.OrderID,
		ConvertedAt:   a.ConvertedAt,
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
	}
	if a.OrderAmount != nil {
		amount := a.OrderAmount.StringFixed(moneyDecimals)
		response.OrderAmount = &amount
	}
	return response
}
