package analyzer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaGenerate(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/chat", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"SELECT 1"},"done":true}`))
	}))
	defer server.Close()

	ollama := NewOllama(OllamaConfig{URL: server.URL + "/", DefaultModel: "m", Timeout: time.Second, ContextSize: 8192, KeepAlive: "30m", MaxTokens: 512})
	reply, err := ollama.Generate(context.Background(), GenerateRequest{
		Model:    "qwen",
		System:   "rules",
		Messages: []Message{{Role: RoleUser, Content: "q"}, {Role: RoleAssistant, Content: "a"}, {Role: RoleUser, Content: "fix"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1", reply)

	assert.Equal(t, map[string]any{
		"model": "qwen",
		"messages": []any{
			map[string]any{"role": "system", "content": "rules"},
			map[string]any{"role": "user", "content": "q"},
			map[string]any{"role": "assistant", "content": "a"},
			map[string]any{"role": "user", "content": "fix"},
		},
		"stream":     false,
		"think":      false,
		"keep_alive": "30m",
		"options":    map[string]any{"temperature": float64(0), "num_ctx": float64(8192), "num_predict": float64(512)},
	}, got)
}

func TestOllamaModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tags", r.URL.Path)
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:latest"},{"name":"llama3:8b"}]}`))
	}))
	defer server.Close()

	models, err := NewOllama(OllamaConfig{URL: server.URL}).Models(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"qwen2.5-coder:latest", "llama3:8b"}, models)
}

func TestOllamaFailuresAreUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model 'nope' not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	_, err := NewOllama(OllamaConfig{URL: server.URL, Timeout: time.Second}).Generate(context.Background(), GenerateRequest{Model: "nope"})
	require.ErrorIs(t, err, ErrLLMUnavailable)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "not found")

	server.Close()
	_, err = NewOllama(OllamaConfig{URL: server.URL}).Models(context.Background())
	assert.ErrorIs(t, err, ErrLLMUnavailable)
}
