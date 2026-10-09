// Command shipment runs the Shipment Service: the fulfillment engine that
// assigns carriers and tracking numbers and ships boxes after a simulated
// packing delay.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/services/shipment/internal/shipment"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "shipment-service"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Kubernetes stops pods with SIGTERM. Cancelling the context lets serve
	// drain in-flight requests and stop the packing loop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCommand().ExecuteContext(ctx)
}

func newRootCommand() *cobra.Command {
	root, v := cli.NewRootCommand("shipment", "Shipment Service: carriers, tracking numbers and simulated packing")
	root.AddCommand(
		newServeCommand(v),
		migrate.NewCommand(serviceName, v, shipment.Migrate),
	)
	return root
}
