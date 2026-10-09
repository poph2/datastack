package shipment

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

// codeShipmentNotFound is returned, in addition to the shared codes in
// httpserver, when an order has no shipment.
const codeShipmentNotFound = "SHIPMENT_NOT_FOUND"

// Store is the persistence the handler needs. *Repository implements it, and
// tests use an in-memory fake.
type Store interface {
	CreateOrGet(ctx context.Context, s Shipment) (Shipment, bool, error)
	GetByOrderID(ctx context.Context, orderID uint64) (Shipment, error)
}

// Handler serves the Shipment Service REST API.
type Handler struct {
	store   Store
	planner *Planner
}

// NewHandler returns a Handler. planner decides carrier, tracking number and
// packing delay for new shipments.
func NewHandler(store Store, planner *Planner) *Handler {
	return &Handler{store: store, planner: planner}
}

// RegisterRoutes adds the shipment endpoints to api.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.POST("/shipments/create", h.create)
	api.GET("/shipments/:order_id", h.get)
}

type createShipmentRequest struct {
	OrderID            uint64 `json:"order_id" binding:"required,min=1"`
	DestinationCountry string `json:"destination_country" binding:"required,iso3166_1_alpha2"`
	// Not stored: it only shortens the packing delay for GOLD customers.
	LoyaltyTier string `json:"loyalty_tier" binding:"omitempty,oneof=BRONZE SILVER GOLD"`
}

type orderURI struct {
	OrderID uint64 `uri:"order_id" binding:"required,min=1"`
}

// shipmentResponse is the create response: what the caller needs right away.
type shipmentResponse struct {
	ShipmentID         uint64  `json:"shipment_id"`
	OrderID            uint64  `json:"order_id"`
	Carrier            Carrier `json:"carrier"`
	TrackingNumber     string  `json:"tracking_number"`
	Status             Status  `json:"status"`
	DestinationCountry string  `json:"destination_country"`
}

// shipmentDetailResponse adds the timeline for drill-down reads.
type shipmentDetailResponse struct {
	shipmentResponse
	PackingDelayMS int        `json:"packing_delay_ms"`
	CreatedAt      time.Time  `json:"created_at"`
	ShippedAt      *time.Time `json:"shipped_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// create accepts a paid order for fulfillment and answers immediately with
// 202: the box ships later, when the packer gets to it. Repeating the call for
// the same order returns the existing shipment, so the Order Service can
// retry safely.
func (h *Handler) create(c *gin.Context) {
	var req createShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	plan := h.planner.Plan(req.DestinationCountry, req.LoyaltyTier)
	shipment, created, err := h.store.CreateOrGet(c.Request.Context(), Shipment{
		OrderID:            req.OrderID,
		Carrier:            plan.Carrier,
		TrackingNumber:     plan.TrackingNumber,
		Status:             StatusPending,
		DestinationCountry: req.DestinationCountry,
		PackingDelayMS:     plan.PackingDelayMS,
	})
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	if created {
		shipmentsCreatedTotal.WithLabelValues(string(shipment.Carrier)).Inc()
	}
	annotateSpan(c, shipment, attribute.Bool("shipment.created", created))
	c.JSON(http.StatusAccepted, toResponse(shipment))
}

func (h *Handler) get(c *gin.Context) {
	var uri orderURI
	if err := c.ShouldBindUri(&uri); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}

	shipment, err := h.store.GetByOrderID(c.Request.Context(), uri.OrderID)
	if errors.Is(err, ErrNotFound) {
		httpserver.RespondError(c, http.StatusNotFound, codeShipmentNotFound, "no shipment for this order")
		return
	}
	if err != nil {
		httpserver.RespondInternalError(c, err)
		return
	}

	annotateSpan(c, shipment)
	c.JSON(http.StatusOK, toDetailResponse(shipment))
}

// annotateSpan records the shipment on the request span, so the checkout
// trace shows which carrier took the order and how long packing will take.
func annotateSpan(c *gin.Context, s Shipment, extra ...attribute.KeyValue) {
	span := trace.SpanFromContext(c.Request.Context())
	span.SetAttributes(shipmentAttributes(s)...)
	span.SetAttributes(extra...)
}

func toResponse(s Shipment) shipmentResponse {
	return shipmentResponse{
		ShipmentID:         s.ID,
		OrderID:            s.OrderID,
		Carrier:            s.Carrier,
		TrackingNumber:     s.TrackingNumber,
		Status:             s.Status,
		DestinationCountry: s.DestinationCountry,
	}
}

func toDetailResponse(s Shipment) shipmentDetailResponse {
	return shipmentDetailResponse{
		shipmentResponse: toResponse(s),
		PackingDelayMS:   s.PackingDelayMS,
		CreatedAt:        s.CreatedAt,
		ShippedAt:        s.ShippedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}
