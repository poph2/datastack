package analyzer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ProviderAnthropic is the --llm-provider value for the Claude API.
const ProviderAnthropic = "anthropic"

const (
	// anthropicMaxTokens leaves room for adaptive thinking, which counts
	// against max_tokens, before the query itself.
	anthropicMaxTokens = 16000
	anthropicListLimit = 100
)

// AnthropicConfig sets how the Claude API is called. The API key comes from
// ANTHROPIC_API_KEY, which the SDK reads itself.
type AnthropicConfig struct {
	DefaultModel string
	Timeout      time.Duration
}

// Anthropic generates SQL with a Claude model.
type Anthropic struct {
	config AnthropicConfig
	client anthropic.Client
}

// NewAnthropic returns an Anthropic generator.
func NewAnthropic(config AnthropicConfig) *Anthropic {
	return &Anthropic{
		config: config,
		client: anthropic.NewClient(option.WithRequestTimeout(config.Timeout)),
	}
}

// Provider implements Generator.
func (a *Anthropic) Provider() string { return ProviderAnthropic }

// DefaultModel implements Generator.
func (a *Anthropic) DefaultModel() string { return a.config.DefaultModel }

// Models implements Generator with the models the API key can use.
func (a *Anthropic) Models(ctx context.Context) ([]string, error) {
	pager := a.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{Limit: anthropic.Int(anthropicListLimit)})
	var models []string
	for pager.Next() {
		models = append(models, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("%w: listing anthropic models: %w", ErrLLMUnavailable, err)
	}
	return models, nil
}

// Generate implements Generator. Current Claude models take no temperature
// and always think adaptively; the server-side refusal fallback lets another
// model answer if a safety classifier declines the request.
func (a *Anthropic) Generate(ctx context.Context, req GenerateRequest) (string, error) {
	messages := make([]anthropic.BetaMessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := anthropic.NewBetaTextBlock(m.Content)
		if m.Role == RoleAssistant {
			messages = append(messages, anthropic.BetaMessageParam{
				Role:    anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{block},
			})
			continue
		}
		messages = append(messages, anthropic.NewBetaUserMessage(block))
	}

	resp, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: anthropicMaxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: req.System}},
		Messages:  messages,
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
	})
	if err != nil {
		return "", fmt.Errorf("%w: anthropic messages: %w", ErrLLMUnavailable, err)
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", fmt.Errorf("%w: the model declined the request", ErrLLMUnavailable)
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if b, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	return text.String(), nil
}
