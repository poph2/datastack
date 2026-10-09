package analyzer

import (
	"context"
	"errors"
)

// ErrLLMUnavailable wraps every failure to get a reply from the model
// provider (network, timeout, HTTP error, refusal). It maps to 502.
var ErrLLMUnavailable = errors.New("llm unavailable")

// Role of a chat message.
type Role string

// Chat roles shared by every provider.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of the conversation sent to the model.
type Message struct {
	Role    Role
	Content string
}

// GenerateRequest is one completion: a system prompt and the turns so far.
type GenerateRequest struct {
	Model    string
	System   string
	Messages []Message
}

// Generator is an LLM provider (Ollama, Anthropic).
type Generator interface {
	// Provider names the provider for metrics, traces and GET /models.
	Provider() string
	// DefaultModel is used when a request names no model.
	DefaultModel() string
	// Models lists the models a request may pick.
	Models(ctx context.Context) ([]string, error)
	// Generate returns the model's reply text.
	Generate(ctx context.Context, req GenerateRequest) (string, error)
}
