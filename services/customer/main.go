// Command customer runs the Customer Service: the source of truth for user
// identity, geography, loyalty tier and account state.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/services/customer/internal/customer"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "customer-service"

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
	root, v := cli.NewRootCommand("customer", "Customer Service: identity, geography, loyalty tier and account state")
	root.AddCommand(
		newServeCommand(v),
		migrate.NewCommand(serviceName, v, customer.Migrate),
	)
	return root
}
