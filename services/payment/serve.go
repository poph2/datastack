package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/httpserver"
	"github.com/poph2/datastack/pkgs/logging"
	"github.com/poph2/datastack/pkgs/telemetry"
	"github.com/poph2/datastack/services/payment/internal/payment"
)

const (
	flagHTTPAddr      = "http-addr"
	flagFailureRate   = "failure-rate"
	flagLatencyMin    = "latency-min"
	flagLatencyMax    = "latency-max"
	flagMethodWeights = "method-weights"

	tracingFlushTimeout = 5 * time.Second
)

func newServeCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), v)
		},
	}

	cmd.Flags().String(flagHTTPAddr, ":8084", "address the HTTP API listens on")
	cmd.Flags().Float64(flagFailureRate, 0.05, "share of charges declined, between 0 and 1")
	cmd.Flags().Duration(flagLatencyMin, 20*time.Millisecond, "minimum simulated gateway latency")
	cmd.Flags().Duration(flagLatencyMax, 150*time.Millisecond, "maximum simulated gateway latency (0 disables)")
	cmd.Flags().String(flagMethodWeights, "CREDIT_CARD=75,PAYPAL=25",
		"relative weights for the method given to charges made without one")
	return cmd
}

func serve(ctx context.Context, v *viper.Viper) error {
	logger, err := logging.New(serviceName, v.GetString(cli.FlagLogLevel), v.GetString(cli.FlagLogFormat))
	if err != nil {
		return err
	}

	methods, err := payment.ParseMethodPicker(v.GetString(flagMethodWeights))
	if err != nil {
		return err
	}

	gateway, err := payment.NewGateway(payment.GatewayConfig{
		FailureRate: v.GetFloat64(flagFailureRate),
		LatencyMin:  v.GetDuration(flagLatencyMin),
		LatencyMax:  v.GetDuration(flagLatencyMax),
	})
	if err != nil {
		return err
	}

	shutdownTracing, err := telemetry.SetupTracing(ctx, serviceName)
	if err != nil {
		return err
	}
	defer flushTraces(shutdownTracing, logger)

	db, err := database.Open(ctx, v.GetString(cli.FlagDatabaseURL))
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Error("closing database", "error", err)
		}
	}()

	handler := payment.NewHandler(payment.NewRepository(db), gateway, methods)
	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    serviceName,
		Logger:         logger,
		Ready:          func(ctx context.Context) error { return database.Ping(ctx, db) },
		RegisterRoutes: handler.RegisterRoutes,
	})

	return httpserver.Run(ctx, v.GetString(flagHTTPAddr), router, logger)
}

// flushTraces exports spans still buffered at shutdown. It uses a fresh
// context because the serve context is already cancelled by then.
func flushTraces(shutdown telemetry.ShutdownFunc, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), tracingFlushTimeout)
	defer cancel()

	if err := shutdown(ctx); err != nil {
		logger.Error("flushing traces", "error", err)
	}
}
