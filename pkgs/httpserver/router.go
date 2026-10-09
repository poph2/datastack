// Package httpserver provides the gin router and HTTP server setup shared by
// every service: tracing, request logging, metrics, panic recovery, health
// probes, and graceful shutdown.
package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	sloggin "github.com/samber/slog-gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// RouterConfig describes what a service plugs into the shared router.
type RouterConfig struct {
	ServiceName string
	Logger      *slog.Logger
	// Ready reports whether the service can take traffic (typically a DB ping).
	Ready func(context.Context) error
	// RegisterRoutes adds the service's API endpoints.
	RegisterRoutes func(api gin.IRouter)
}

// NewRouter builds the gin engine.
//
// Probe and metrics endpoints are registered on the bare engine. API routes go
// on a group carrying the tracing, logging and metrics middleware, so a
// kubelet probing every few seconds doesn't flood the traces and logs.
func NewRouter(cfg RouterConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()
	engine.Use(recovery(cfg.Logger))

	engine.GET("/healthz", healthz)
	engine.GET("/readyz", readyz(cfg.Ready))
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	api := engine.Group("/",
		otelgin.Middleware(cfg.ServiceName),
		sloggin.NewWithConfig(cfg.Logger, requestLogConfig()),
		requestMetrics(),
	)
	cfg.RegisterRoutes(api)

	return engine
}

func requestLogConfig() sloggin.Config {
	config := sloggin.DefaultConfig()
	config.ClientErrorLevel = slog.LevelWarn
	config.ServerErrorLevel = slog.LevelError
	config.WithTraceID = true
	config.WithSpanID = true
	return config
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func readyz(ready func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := ready(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	}
}

func recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "panic while handling request",
			"panic", recovered, "method", c.Request.Method, "path", c.Request.URL.Path)
		RespondError(c, http.StatusInternalServerError, CodeInternal, "internal server error")
	})
}
