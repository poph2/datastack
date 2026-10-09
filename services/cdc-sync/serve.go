package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/httpserver"
	"github.com/poph2/datastack/pkgs/logging"
	"github.com/poph2/datastack/pkgs/telemetry"
	"github.com/poph2/datastack/services/cdc-sync/internal/cdc"
)

const (
	flagHTTPAddr           = "http-addr"
	flagPostgresURL        = "postgres-url"
	flagTables             = "tables"
	flagStarRocksMySQLAddr = "starrocks-mysql-addr"
	flagStarRocksLoadURL   = "starrocks-load-url"
	flagStarRocksDB        = "starrocks-db"
	flagStarRocksUser      = "starrocks-user"
	flagStarRocksPassword  = "starrocks-password"
	flagPollInterval       = "poll-interval"
	flagBatchSize          = "batch-size"
	flagSafetyLag          = "safety-lag"
	flagSchemaRefresh      = "schema-refresh-interval"

	defaultTables = "customers.customers,marketing.campaigns,marketing.attributions," +
		"inventory.inventory,payments.payments,shipments.shipments,orders.orders"

	tracingFlushTimeout = 5 * time.Second
)

func newServeCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Sync every configured table into StarRocks and serve /status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), v)
		},
	}

	flags := cmd.Flags()
	flags.String(flagHTTPAddr, ":8090", "address the HTTP endpoints listen on")
	flags.String(flagPostgresURL, "postgres://localhost:5433/?sslmode=disable",
		"Postgres URL without a database; each source database is set per table (credentials may come from PGUSER/PGPASSWORD)")
	flags.String(flagTables, defaultTables, "comma-separated <database>.<table> list to sync")
	flags.String(flagStarRocksMySQLAddr, "localhost:9030", "StarRocks FE MySQL-protocol address, for DDL and cursors")
	flags.String(flagStarRocksLoadURL, "http://localhost:8040", "StarRocks BE HTTP URL that receives Stream Loads")
	flags.String(flagStarRocksDB, "analytics_db", "StarRocks database the tables are synced into")
	flags.String(flagStarRocksUser, "root", "StarRocks user")
	flags.String(flagStarRocksPassword, "", "StarRocks password")
	flags.Duration(flagPollInterval, 5*time.Second, "time between polls of a caught-up table")
	flags.Int(flagBatchSize, 1000, "most rows read and loaded per batch")
	flags.Duration(flagSafetyLag, 2*time.Second, "leave rows changed more recently than this for a later poll")
	flags.Duration(flagSchemaRefresh, time.Minute, "time between re-reads of each source schema, which carry changed comments into StarRocks")
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

	tables, err := cdc.ParseTables(v.GetString(flagTables))
	if err != nil {
		return err
	}

	sources, err := cdc.NewSources(v.GetString(flagPostgresURL))
	if err != nil {
		return err
	}
	defer sources.Close()

	catalog, err := cdc.OpenStarRocks(cdc.StarRocksConfig{
		Addr:     v.GetString(flagStarRocksMySQLAddr),
		Database: v.GetString(flagStarRocksDB),
		User:     v.GetString(flagStarRocksUser),
		Password: v.GetString(flagStarRocksPassword),
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := catalog.Close(); err != nil {
			logger.Error("closing starrocks connection pool", "error", err)
		}
	}()

	loader := cdc.NewStreamLoader(cdc.StreamLoadConfig{
		URL:      v.GetString(flagStarRocksLoadURL),
		Database: v.GetString(flagStarRocksDB),
		User:     v.GetString(flagStarRocksUser),
		Password: v.GetString(flagStarRocksPassword),
	})

	tracker := cdc.NewTracker(tables)
	syncer, err := cdc.NewSyncer(tables, cdc.Dependencies{
		Sources: sources,
		Catalog: catalog,
		Loader:  loader,
		Tracker: tracker,
		Logger:  logger,
	}, cdc.SyncConfig{
		PollInterval:          v.GetDuration(flagPollInterval),
		BatchSize:             v.GetInt(flagBatchSize),
		SafetyLag:             v.GetDuration(flagSafetyLag),
		SchemaRefreshInterval: v.GetDuration(flagSchemaRefresh),
	})
	if err != nil {
		return err
	}

	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    serviceName,
		Logger:         logger,
		Ready:          catalog.Ping,
		RegisterRoutes: cdc.NewStatusHandler(tracker).RegisterRoutes,
	})

	logger.Info("cdc sync starting", "tables", len(tables), "starrocks_load_url", v.GetString(flagStarRocksLoadURL))

	// The HTTP server and the table loops share one lifetime. A shutdown
	// signal stops both, and if the server fails (for example the port is
	// taken) the loops stop too. Neither StarRocks nor any source has to be
	// up for the daemon to start: each loop retries its own setup.
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		syncer.Run(groupCtx)
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
