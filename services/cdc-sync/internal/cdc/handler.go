package cdc

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// StatusHandler serves the sync progress of every table.
type StatusHandler struct {
	tracker *Tracker
}

// NewStatusHandler returns a StatusHandler reading from tracker.
func NewStatusHandler(tracker *Tracker) *StatusHandler {
	return &StatusHandler{tracker: tracker}
}

// RegisterRoutes adds GET /status to api.
func (h *StatusHandler) RegisterRoutes(api gin.IRouter) {
	api.GET("/status", h.status)
}

func (h *StatusHandler) status(c *gin.Context) {
	c.JSON(http.StatusOK, h.tracker.Snapshot())
}
