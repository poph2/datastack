package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/httpserver"
	"github.com/poph2/datastack/pkgs/logging"
	"github.com/poph2/datastack/pkgs/telemetry"
	"github.com/poph2/datastack/services/inventory/internal/inventory"
)

const (
	flagHTTPAddr         = "http-addr"
	flagRestockInterval  = "restock-interval"
	flagRestockThreshold = "restock-threshold"
	flagRestockQuantity  = "restock-quantity"

	tracingFlushTimeout = 5 * time.Second
)

func newServeCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API and the background restock loop",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), v)
		},
	}

	cmd.Flags().String(flagHTTPAddr, ":8083", "address the HTTP API listens on")
	cmd.Flags().Duration(flagRestockInterval, 2*time.Minute, "how often to restock products running low (0 disables)")
	cmd.Flags().Int(flagRestockThreshold, 10, "restock products with fewer units than this")
	cmd.Flags().Int(flagRestockQuantity, 50, "units added to each restocked product (must be >= the threshold)")
	return cmd
}

func serve(ctx context.Context, v *viper.Viper) error {
	logger, err := logging.New(serviceName, v.GetString(cli.FlagLogLevel), v.GetString(cli.FlagLogFormat))
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

	repo := inventory.NewRepository(db)

	restocker, err := inventory.NewRestocker(repo, inventory.RestockConfig{
		Interval:  v.GetDuration(flagRestockInterval),
		Threshold: v.GetInt(flagRestockThreshold),
		Quantity:  v.GetInt(flagRestockQuantity),
	}, logger)
	if err != nil {
		return err
	}

	handler := inventory.NewHandler(repo)
	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    serviceName,
		Logger:         logger,
		Ready:          func(ctx context.Context) error { return database.Ping(ctx, db) },
		RegisterRoutes: handler.RegisterRoutes,
	})

	// The HTTP server and the restock loop share one lifetime. A shutdown
	// signal stops both, and if the server fails (for example the port is
	// taken) the loop stops too. Wait returns only once both are done, so the
	// database closes after the last restock query.
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		restocker.Run(groupCtx)
		return nil
	})
	group.Go(func() error {
		return httpserver.Run(groupCtx, v.GetString(flagHTTPAddr), router, logger)
	})
	return group.Wait()
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
