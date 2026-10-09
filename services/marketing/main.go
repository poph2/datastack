// Command marketing runs the Marketing Service: campaigns, touchpoint
// attribution and the conversion handshake that links a click to an order.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/services/marketing/internal/marketing"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "marketing-service"

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
	root, v := cli.NewRootCommand("marketing", "Marketing Service: campaigns, touchpoint attribution and conversions")
	root.AddCommand(
		newServeCommand(v),
		migrate.NewCommand(serviceName, v, marketing.Migrate),
	)
	return root
}
