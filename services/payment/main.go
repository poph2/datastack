// Command payment runs the Payment Service: a simulated card gateway that
// charges orders, declines some of them on purpose, and records every attempt.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/services/payment/internal/payment"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "payment-service"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Kubernetes stops pods with SIGTERM. Cancelling the context lets serve
	// drain in-flight requests.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCommand().ExecuteContext(ctx)
}

func newRootCommand() *cobra.Command {
	root, v := cli.NewRootCommand("payment", "Payment Service: simulated card processing with injected failures")
	root.AddCommand(
		newServeCommand(v),
		migrate.NewCommand(serviceName, v, payment.Migrate),
	)
	return root
}
