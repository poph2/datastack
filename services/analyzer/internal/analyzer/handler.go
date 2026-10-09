package analyzer

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/httpserver"
)

// maxQuestionLength keeps a question to a sentence or a short paragraph, so
// nobody can stuff a document into the prompt.
const maxQuestionLength = 2000

// Asking is what the handler needs from the pipeline. *Asker implements it.
type Asking interface {
	Ask(ctx context.Context, question, model string) (*Answer, error)
	Generator() Generator
}

// Handler serves the analyzer's HTTP API.
type Handler struct {
	asker   Asking
	schemas Schemas
}

// NewHandler returns a Handler.
func NewHandler(asker Asking, schemas Schemas) *Handler {
	return &Handler{asker: asker, schemas: schemas}
}

// RegisterRoutes adds the API endpoints.
func (h *Handler) RegisterRoutes(api gin.IRouter) {
	api.GET("/models", h.models)
	api.GET("/schema", h.schema)
	api.POST("/ask", h.ask)
}

type modelsResponse struct {
	Provider string   `json:"provider"`
	Default  string   `json:"default"`
	Models   []string `json:"models"`
}

func (h *Handler) models(c *gin.Context) {
	generator := h.asker.Generator()
	models, err := generator.Models(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		httpserver.RespondError(c, http.StatusBadGateway, CodeLLMUnavailable, err.Error())
		return
	}
	c.JSON(http.StatusOK, modelsResponse{Provider: generator.Provider(), Default: generator.DefaultModel(), Models: models})
}

type schemaResponse struct {
	Tables   []Table `json:"tables"`
	Joins    []Join  `json:"joins"`
	LoadedAt string  `json:"loaded_at"`
}

func (h *Handler) schema(c *gin.Context) {
	schema, err := h.schemas.Current()
	if err != nil {
		httpserver.RespondError(c, http.StatusServiceUnavailable, CodeSchemaNotLoaded, err.Error())
		return
	}
	joins := schema.Joins
	if joins == nil {
		joins = []Join{}
	}
	c.JSON(http.StatusOK, schemaResponse{Tables: schema.Tables, Joins: joins, LoadedAt: schema.LoadedAt.Format(time.RFC3339)})
}

type askRequest struct {
	Question string `json:"question"`
	Model    string `json:"model"`
}

type askResponse struct {
	Question   string         `json:"question"`
	Model      string         `json:"model"`
	SQL        string         `json:"sql"`
	Columns    []ResultColumn `json:"columns"`
	Rows       [][]any        `json:"rows"`
	RowCount   int            `json:"row_count"`
	Truncated  bool           `json:"truncated"`
	Attempts   int            `json:"attempts"`
	DurationMS int64          `json:"duration_ms"`
	TraceID    string         `json:"trace_id"`
}

// askErrorResponse is the shared error body plus the last SQL tried.
type askErrorResponse struct {
	httpserver.ErrorResponse
	SQL string `json:"sql"`
}

func (h *Handler) ask(c *gin.Context) {
	var req askRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpserver.RespondInvalidRequest(c, err)
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		httpserver.RespondError(c, http.StatusBadRequest, httpserver.CodeInvalidRequest, "question is required")
		return
	}
	if len(req.Question) > maxQuestionLength {
		httpserver.RespondError(c, http.StatusBadRequest, httpserver.CodeInvalidRequest, "question is too long")
		return
	}

	answer, err := h.asker.Ask(c.Request.Context(), req.Question, strings.TrimSpace(req.Model))
	if err != nil {
		respondAskError(c, err)
		return
	}

	c.JSON(http.StatusOK, askResponse{
		Question:   answer.Question,
		Model:      answer.Model,
		SQL:        answer.SQL,
		Columns:    answer.Result.Columns,
		Rows:       answer.Result.Rows,
		RowCount:   len(answer.Result.Rows),
		Truncated:  answer.Result.Truncated,
		Attempts:   answer.Attempts,
		DurationMS: answer.Duration.Milliseconds(),
		TraceID:    traceID(c.Request.Context()),
	})
}

func respondAskError(c *gin.Context, err error) {
	var failure *AskError
	switch {
	case errors.Is(err, ErrUnknownModel):
		httpserver.RespondError(c, http.StatusBadRequest, httpserver.CodeInvalidRequest, err.Error())
	case errors.Is(err, ErrSchemaNotLoaded):
		httpserver.RespondError(c, http.StatusServiceUnavailable, CodeSchemaNotLoaded, err.Error())
	case errors.As(err, &failure) && failure.Code == CodeLLMUnavailable:
		_ = c.Error(err)
		httpserver.RespondError(c, http.StatusBadGateway, CodeLLMUnavailable, failure.Message)
	case errors.As(err, &failure):
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, askErrorResponse{
			ErrorResponse: httpserver.ErrorResponse{Error: failure.Code, Message: failure.Message},
			SQL:           failure.SQL,
		})
	default:
		httpserver.RespondInternalError(c, err)
	}
}

// traceID returns the ID of the request's trace, so the UI can link to it
// in Tempo. It is empty when tracing is off.
func traceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.HasTraceID() {
		return ""
	}
	return spanContext.TraceID().String()
}
