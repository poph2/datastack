package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ProviderOllama is the --llm-provider value for a local Ollama.
const ProviderOllama = "ollama"

// ollamaListTimeout bounds GET /api/tags, which answers from disk.
const ollamaListTimeout = 5 * time.Second

// maxErrorBody caps how much of an error response is quoted in an error.
const maxErrorBody = 512

// OllamaConfig locates an Ollama server and sets how it is called.
type OllamaConfig struct {
	URL          string
	DefaultModel string
	// Timeout bounds one generation. A 7B model on a laptop CPU is slow.
	Timeout time.Duration
	// ContextSize is num_ctx. Ollama's default (2048 to 4096 tokens)
	// silently truncates the schema prompt, so it is always set.
	ContextSize int
	// KeepAlive is how long Ollama keeps the model loaded after a request
	// (a Go duration such as "30m"). While it is loaded, Ollama reuses the
	// cached system prompt, which is identical on every request, so only
	// the question has to be processed. On a CPU-only machine that is the
	// difference between seconds and minutes.
	KeepAlive string
}

// Ollama generates SQL with a model served by Ollama's /api/chat.
type Ollama struct {
	config OllamaConfig
	client *http.Client
}

// NewOllama returns an Ollama generator.
func NewOllama(config OllamaConfig) *Ollama {
	return &Ollama{
		config: config,
		client: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

// Provider implements Generator.
func (o *Ollama) Provider() string { return ProviderOllama }

// DefaultModel implements Generator.
func (o *Ollama) DefaultModel() string { return o.config.DefaultModel }

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	// Think false turns off the reasoning phase of thinking models
	// (qwen3.5), which is slow and isn't needed to write one query.
	// Models without thinking ignore it.
	Think     bool          `json:"think"`
	KeepAlive string        `json:"keep_alive,omitempty"`
	Options   ollamaOptions `json:"options"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// Generate implements Generator: one non-streaming chat at temperature 0.
func (o *Ollama) Generate(ctx context.Context, req GenerateRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.config.Timeout)
	defer cancel()

	messages := []ollamaMessage{{Role: "system", Content: req.System}}
	for _, m := range req.Messages {
		messages = append(messages, ollamaMessage{Role: string(m.Role), Content: m.Content})
	}
	body := ollamaChatRequest{
		Model:     req.Model,
		Messages:  messages,
		KeepAlive: o.config.KeepAlive,
		Options:   ollamaOptions{Temperature: 0, NumCtx: o.config.ContextSize},
	}

	var reply ollamaChatResponse
	if err := o.call(ctx, http.MethodPost, "/api/chat", body, &reply); err != nil {
		return "", err
	}
	return reply.Message.Content, nil
}

// Models implements Generator with the models pulled into this Ollama.
func (o *Ollama) Models(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, ollamaListTimeout)
	defer cancel()

	var tags ollamaTagsResponse
	if err := o.call(ctx, http.MethodGet, "/api/tags", nil, &tags); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		models = append(models, m.Name)
	}
	return models, nil
}

// call sends a JSON request and decodes the JSON reply. Every failure wraps
// ErrLLMUnavailable.
func (o *Ollama) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding ollama request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.config.URL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("%w: building ollama request: %w", ErrLLMUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: ollama %s: %w", ErrLLMUnavailable, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("%w: ollama %s returned %d: %s", ErrLLMUnavailable, path, resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decoding ollama %s response: %w", ErrLLMUnavailable, path, err)
	}
	return nil
}
