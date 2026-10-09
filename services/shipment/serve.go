package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/poph2/datastack/pkgs/chaos"
	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/httpserver"
	"github.com/poph2/datastack/pkgs/logging"
	"github.com/poph2/datastack/pkgs/telemetry"
	"github.com/poph2/datastack/services/shipment/internal/shipment"
)

const (
	flagHTTPAddr        = "http-addr"
	flagPackingDelayMin = "packing-delay-min"
	flagPackingDelayMax = "packing-delay-max"
	flagGoldDelayFactor = "gold-delay-factor"
	flagShipInterval    = "ship-interval"
	flagShipCapacity    = "ship-capacity"

	tracingFlushTimeout = 5 * time.Second
)

func newServeCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API and the background packing loop",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), v)
		},
	}

	cmd.Flags().String(flagHTTPAddr, ":8085", "address the HTTP API listens on")
	cmd.Flags().Duration(flagPackingDelayMin, 2*time.Second, "shortest packing delay for a new shipment")
	cmd.Flags().Duration(flagPackingDelayMax, 30*time.Second, "longest packing delay for a new shipment")
	cmd.Flags().Float64(flagGoldDelayFactor, 0.5, "multiplier in (0, 1] applied to the packing delay of GOLD customers")
	cmd.Flags().Duration(flagShipInterval, time.Second, "how often to ship shipments whose packing delay has passed (0 disables)")
	cmd.Flags().Int(flagShipCapacity, 10, "most shipments shipped per run, per replica (default of the ship_capacity chaos knob)")
	return cmd
}

func serve(ctx context.Context, v *viper.Viper) error {
	logger, err := logging.New(serviceName, v.GetString(cli.FlagLogLevel), v.GetString(cli.FlagLogFormat))
	if err != nil {
		return err
	}

	planner, err := shipment.NewPlanner(shipment.PlanConfig{
		PackingDelayMin: v.GetDuration(flagPackingDelayMin),
		PackingDelayMax: v.GetDuration(flagPackingDelayMax),
		GoldDelayFactor: v.GetFloat64(flagGoldDelayFactor),
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

	repo := shipment.NewRepository(db)

	packer, err := shipment.NewPacker(repo, shipment.PackConfig{
		Interval: v.GetDuration(flagShipInterval),
		Capacity: v.GetInt(flagShipCapacity),
	}, logger)
	if err != nil {
		return err
	}

	chaosKnobs, err := chaos.NewStore(serviceName, logger, append(planner.ChaosKnobs(), packer.ChaosKnobs()...)...)
	if err != nil {
		return err
	}

	handler := shipment.NewHandler(repo, planner)
	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    serviceName,
		Logger:         logger,
		Ready:          func(ctx context.Context) error { return database.Ping(ctx, db) },
		RegisterRoutes: chaosKnobs.Routes(handler.RegisterRoutes),
	})

	// The HTTP server and the packing loop share one lifetime. A shutdown
	// signal stops both, and if the server fails (for example the port is
	// taken) the loop stops too. Wait returns only once both are done, so the
	// database closes after the last packing query.
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		packer.Run(groupCtx)
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
