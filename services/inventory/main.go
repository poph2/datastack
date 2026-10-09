// Command inventory runs the Inventory Service: the source of truth for
// product stock, prices and reservations.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/services/inventory/internal/inventory"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "inventory-service"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Kubernetes stops pods with SIGTERM. Cancelling the context lets serve
	// drain in-flight requests and stop the restock loop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCommand().ExecuteContext(ctx)
}

func newRootCommand() *cobra.Command {
	root, v := cli.NewRootCommand("inventory", "Inventory Service: product stock, prices and reservations")
	root.AddCommand(
		newServeCommand(v),
		migrate.NewCommand(serviceName, v, inventory.Migrate),
	)
	return root
}
