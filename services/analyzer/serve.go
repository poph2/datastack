package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/poph2/datastack/pkgs/httpserver"
	"github.com/poph2/datastack/services/analyzer/internal/analyzer"
)

const (
	flagHTTPAddr = "http-addr"
	flagWarmUp   = "warm-up"

	// warmUpAttempts bounds the start-up warm-up. Ollama keeps the part of
	// the prompt it processed before a timeout, so each attempt gets further.
	warmUpAttempts = 3
	warmUpPoll     = 5 * time.Second
)

func newServeCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the question-answering API and keep the schema fresh",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), v)
		},
	}

	flags := cmd.Flags()
	flags.String(flagHTTPAddr, ":8086", "address the HTTP API listens on")
	flags.Bool(flagWarmUp, true, "with ollama, ask one throwaway question at start-up so the model caches the schema prompt")
	addPipelineFlags(flags)
	return cmd
}

func serve(ctx context.Context, v *viper.Viper) error {
	p, err := newPipeline(ctx, v)
	if err != nil {
		return err
	}
	defer p.shutdown()

	handler := analyzer.NewHandler(p.asker, p.schemas)
	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    serviceName,
		Logger:         p.logger,
		Ready:          p.schemas.Ready,
		RegisterRoutes: handler.RegisterRoutes,
	})

	p.logger.Info("analyzer starting",
		"llm_provider", p.asker.Generator().Provider(), "default_model", p.asker.Generator().DefaultModel(),
		"starrocks_addr", v.GetString(flagStarRocksAddr))

	// The HTTP server and the schema refresh share one lifetime. StarRocks
	// doesn't have to be up: the service stays unready until the first
	// schema load succeeds.
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		p.schemas.Run(groupCtx)
		return nil
	})
	if v.GetBool(flagWarmUp) && p.asker.Generator().Provider() == analyzer.ProviderOllama {
		group.Go(func() error {
			warmUp(groupCtx, p)
			return nil
		})
	}
	group.Go(func() error {
		return httpserver.Run(groupCtx, v.GetString(flagHTTPAddr), router, p.logger)
	})
	return group.Wait()
}

// warmUp waits for the first schema load, then warms the model's prompt
// cache. Failures are only logged: questions still work, just slowly.
func warmUp(ctx context.Context, p *pipeline) {
	for p.schemas.Ready(ctx) != nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(warmUpPoll):
		}
	}

	for attempt := 1; attempt <= warmUpAttempts; attempt++ {
		start := time.Now()
		err := p.asker.WarmUp(ctx)
		if err == nil {
			p.logger.Info("llm warmed up", "attempt", attempt, "seconds", time.Since(start).Seconds())
			return
		}
		if ctx.Err() != nil {
			return
		}
		p.logger.Warn("llm warm-up failed", "attempt", attempt, "error", err)
	}
}
