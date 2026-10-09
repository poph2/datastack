package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Error codes shared by every service. Domain-specific codes such as
// OUT_OF_STOCK live in the service that returns them.
const (
	CodeInvalidRequest = "INVALID_REQUEST"
	CodeInternal       = "INTERNAL_ERROR"
)

// ErrorResponse is the JSON body of every non-2xx response. Error is a stable,
// machine-readable code that callers branch on (the Order Service stores it as
// an order's failure_reason). Message is for humans.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// RespondError writes an error response and stops the handler chain.
func RespondError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{Error: code, Message: message})
}

// RespondInvalidRequest is used for malformed or invalid input. It returns 400
// with err's text, which for bind errors names the offending field.
func RespondInvalidRequest(c *gin.Context, err error) {
	RespondError(c, http.StatusBadRequest, CodeInvalidRequest, err.Error())
}

// RespondInternalError attaches err to the request (so the request log
// includes it) and returns a generic 500 that leaks no internals.
func RespondInternalError(c *gin.Context, err error) {
	_ = c.Error(err)
	RespondError(c, http.StatusInternalServerError, CodeInternal, "internal server error")
}
