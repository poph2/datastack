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
	"github.com/poph2/datastack/pkgs/httpclient"
	"github.com/poph2/datastack/pkgs/httpserver"
	"github.com/poph2/datastack/pkgs/logging"
	"github.com/poph2/datastack/pkgs/telemetry"
	"github.com/poph2/datastack/services/order/internal/order"
)

const (
	flagHTTPAddr          = "http-addr"
	flagCustomerURL       = "customer-url"
	flagMarketingURL      = "marketing-url"
	flagInventoryURL      = "inventory-url"
	flagPaymentURL        = "payment-url"
	flagShipmentURL       = "shipment-url"
	flagCriticalTimeout   = "critical-timeout"
	flagMarketingTimeout  = "marketing-timeout"
	flagShipmentTimeout   = "shipment-timeout"
	flagReconcileInterval = "reconcile-interval"
	flagReconcileAfter    = "reconcile-after"

	tracingFlushTimeout = 5 * time.Second
)

func newServeCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API and the stuck-order reconciler",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), v)
		},
	}

	flags := cmd.Flags()
	flags.String(flagHTTPAddr, ":8080", "address the HTTP API listens on")
	flags.String(flagCustomerURL, "http://localhost:8081", "Customer Service base URL")
	flags.String(flagMarketingURL, "http://localhost:8082", "Marketing Service base URL")
	flags.String(flagInventoryURL, "http://localhost:8083", "Inventory Service base URL")
	flags.String(flagPaymentURL, "http://localhost:8084", "Payment Service base URL")
	flags.String(flagShipmentURL, "http://localhost:8085", "Shipment Service base URL")
	flags.Duration(flagCriticalTimeout, 2*time.Second, "timeout for customer, inventory and payment calls")
	flags.Duration(flagMarketingTimeout, 500*time.Millisecond, "timeout for the best-effort marketing conversion call")
	flags.Duration(flagShipmentTimeout, time.Second, "timeout for the shipment request")
	flags.Duration(flagReconcileInterval, 30*time.Second, "how often to retry shipments for orders stuck in PAID (0 disables)")
	flags.Duration(flagReconcileAfter, 30*time.Second, "how long an order must be PAID before the reconciler retries it")
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

	critical := v.GetDuration(flagCriticalTimeout)
	peers := order.Peers{
		Customers: order.NewCustomerClient(httpclient.New("customer-service", v.GetString(flagCustomerURL), critical)),
		Inventory: order.NewInventoryClient(httpclient.New("inventory-service", v.GetString(flagInventoryURL), critical)),
		Payments:  order.NewPaymentClient(httpclient.New("payment-service", v.GetString(flagPaymentURL), critical)),
		Marketing: order.NewMarketingClient(httpclient.New("marketing-service", v.GetString(flagMarketingURL), v.GetDuration(flagMarketingTimeout))),
		Shipments: order.NewShipmentClient(httpclient.New("shipment-service", v.GetString(flagShipmentURL), v.GetDuration(flagShipmentTimeout))),
	}

	repo := order.NewRepository(db)
	checkout := order.NewCheckout(repo, peers, logger)

	reconciler, err := order.NewReconciler(repo, checkout, peers.Customers, order.ReconcileConfig{
		Interval: v.GetDuration(flagReconcileInterval),
		After:    v.GetDuration(flagReconcileAfter),
	}, logger)
	if err != nil {
		return err
	}

	handler := order.NewHandler(checkout, repo)
	router := httpserver.NewRouter(httpserver.RouterConfig{
		ServiceName:    serviceName,
		Logger:         logger,
		Ready:          func(ctx context.Context) error { return database.Ping(ctx, db) },
		RegisterRoutes: handler.RegisterRoutes,
	})

	// The HTTP server and the reconciler share one lifetime (see the
	// Inventory Service's serve.go for the same pattern).
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		reconciler.Run(groupCtx)
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
