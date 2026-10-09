package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/logging"
	"github.com/poph2/datastack/pkgs/telemetry"
	"github.com/poph2/datastack/services/analyzer/internal/analyzer"
)

// Flags shared by serve and eval, which run the same pipeline.
const (
	flagStarRocksAddr     = "starrocks-addr"
	flagStarRocksDB       = "starrocks-db"
	flagStarRocksUser     = "starrocks-user"
	flagStarRocksPassword = "starrocks-password"
	flagReadOnlyUser      = "readonly-user"
	flagReadOnlyPassword  = "readonly-password"
	flagSchemaRefresh     = "schema-refresh-interval"
	flagLLMProvider       = "llm-provider"
	flagModel             = "model"
	flagOllamaURL         = "ollama-url"
	flagOllamaNumCtx      = "ollama-num-ctx"
	flagOllamaKeepAlive   = "ollama-keep-alive"
	flagLLMTimeout        = "llm-timeout"
	flagMaxAttempts       = "max-attempts"
	flagMaxRows           = "max-rows"
	flagQueryTimeout      = "query-timeout"

	defaultOllamaModel    = "qwen2.5-coder:latest"
	defaultAnthropicModel = "claude-opus-5-5"

	tracingFlushTimeout = 5 * time.Second
)

func addPipelineFlags(flags *pflag.FlagSet) {
	flags.String(flagStarRocksAddr, "localhost:9030", "StarRocks FE MySQL-protocol address")
	flags.String(flagStarRocksDB, "analytics_db", "StarRocks database the questions are about")
	flags.String(flagStarRocksUser, "root", "StarRocks admin user: reads the schema and manages the read-only user")
	flags.String(flagStarRocksPassword, "", "StarRocks admin password")
	flags.String(flagReadOnlyUser, "analyzer_ro", "StarRocks user that runs generated SQL, granted SELECT on the database only")
	flags.String(flagReadOnlyPassword, "", "password set on the read-only user")
	flags.Duration(flagSchemaRefresh, time.Minute, "time between schema re-reads (and read-only re-grants)")
	flags.String(flagLLMProvider, analyzer.ProviderOllama, "LLM provider: ollama or anthropic (API key from ANTHROPIC_API_KEY)")
	flags.String(flagModel, "", fmt.Sprintf("default model (empty: %s for ollama, %s for anthropic)", defaultOllamaModel, defaultAnthropicModel))
	flags.String(flagOllamaURL, "http://host.docker.internal:11434", "Ollama base URL")
	flags.Int(flagOllamaNumCtx, 8192, "Ollama context window in tokens; must fit the schema prompt")
	flags.String(flagOllamaKeepAlive, "30m", "how long Ollama keeps the model (and the cached schema prompt) loaded between questions")
	flags.Duration(flagLLMTimeout, 60*time.Second, "timeout for one LLM generation")
	flags.Int(flagMaxAttempts, 2, "generations per question: the first plus repairs after a failed query")
	flags.Int(flagMaxRows, 500, "most rows returned per answer; more are reported as truncated")
	flags.Duration(flagQueryTimeout, 15*time.Second, "timeout for EXPLAIN and for running a generated query")
}

// pipeline is everything serve and eval share.
type pipeline struct {
	logger    *slog.Logger
	starRocks *analyzer.StarRocks
	schemas   *analyzer.SchemaStore
	asker     *analyzer.Asker
	shutdown  func()
}

// newPipeline sets up logging, tracing, StarRocks and the LLM. Call
// shutdown when done.
func newPipeline(ctx context.Context, v *viper.Viper) (*pipeline, error) {
	logger, err := logging.New(serviceName, v.GetString(cli.FlagLogLevel), v.GetString(cli.FlagLogFormat))
	if err != nil {
		return nil, err
	}

	shutdownTracing, err := telemetry.SetupTracing(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	starRocks, err := analyzer.OpenStarRocks(analyzer.StarRocksConfig{
		Addr:             v.GetString(flagStarRocksAddr),
		Database:         v.GetString(flagStarRocksDB),
		AdminUser:        v.GetString(flagStarRocksUser),
		AdminPassword:    v.GetString(flagStarRocksPassword),
		ReadOnlyUser:     v.GetString(flagReadOnlyUser),
		ReadOnlyPassword: v.GetString(flagReadOnlyPassword),
		QueryTimeout:     v.GetDuration(flagQueryTimeout),
	})
	if err != nil {
		flushTraces(shutdownTracing, logger)
		return nil, err
	}

	shutdown := func() {
		if err := starRocks.Close(); err != nil {
			logger.Error("closing starrocks connection pools", "error", err)
		}
		flushTraces(shutdownTracing, logger)
	}

	generator, err := newGenerator(v)
	if err != nil {
		shutdown()
		return nil, err
	}

	schemas := analyzer.NewSchemaStore(starRocks, v.GetDuration(flagSchemaRefresh), logger)
	asker, err := analyzer.NewAsker(schemas, generator, starRocks, analyzer.AskConfig{
		MaxAttempts: v.GetInt(flagMaxAttempts),
		MaxRows:     v.GetInt(flagMaxRows),
	}, logger)
	if err != nil {
		shutdown()
		return nil, err
	}

	return &pipeline{logger: logger, starRocks: starRocks, schemas: schemas, asker: asker, shutdown: shutdown}, nil
}

func newGenerator(v *viper.Viper) (analyzer.Generator, error) {
	model := v.GetString(flagModel)
	timeout := v.GetDuration(flagLLMTimeout)

	switch provider := v.GetString(flagLLMProvider); provider {
	case analyzer.ProviderOllama:
		if model == "" {
			model = defaultOllamaModel
		}
		return analyzer.NewOllama(analyzer.OllamaConfig{
			URL:          v.GetString(flagOllamaURL),
			DefaultModel: model,
			Timeout:      timeout,
			ContextSize:  v.GetInt(flagOllamaNumCtx),
			KeepAlive:    v.GetString(flagOllamaKeepAlive),
		}), nil
	case analyzer.ProviderAnthropic:
		if model == "" {
			model = defaultAnthropicModel
		}
		return analyzer.NewAnthropic(analyzer.AnthropicConfig{DefaultModel: model, Timeout: timeout}), nil
	default:
		return nil, fmt.Errorf("unknown --%s %q (want %s or %s)", flagLLMProvider, provider, analyzer.ProviderOllama, analyzer.ProviderAnthropic)
	}
}

// flushTraces exports spans still buffered at shutdown. It uses a fresh
// context because the command's context may already be cancelled.
func flushTraces(shutdown telemetry.ShutdownFunc, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), tracingFlushTimeout)
	defer cancel()

	if err := shutdown(ctx); err != nil {
		logger.Error("flushing traces", "error", err)
	}
}
