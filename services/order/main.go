// Command order runs the Order Service: the edge API that k6 calls, which
// orchestrates every checkout across the other services.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/services/order/internal/order"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "order-service"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Kubernetes stops pods with SIGTERM. Cancelling the context lets serve
	// drain in-flight checkouts and stop the reconciler.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCommand().ExecuteContext(ctx)
}

func newRootCommand() *cobra.Command {
	root, v := cli.NewRootCommand("order", "Order Service: checkout orchestration and the order record")
	root.AddCommand(
		newServeCommand(v),
		migrate.NewCommand(serviceName, v, order.Migrate),
	)
	return root
}
