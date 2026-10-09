// Command cdc-sync runs the CDC sync daemon: it copies every insert, update
// and soft delete from the operational Postgres databases into StarRocks.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/poph2/datastack/pkgs/cli"
)

// serviceName identifies this service in logs, traces and metrics.
const serviceName = "cdc-sync"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Kubernetes stops pods with SIGTERM. Cancelling the context stops the
	// table loops and drains the HTTP server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCommand().ExecuteContext(ctx)
}

// newRootCommand has only serve: the daemon owns no database, so there is
// nothing to migrate.
func newRootCommand() *cobra.Command {
	root, v := cli.NewRootCommand("cdc-sync", "CDC sync daemon: Postgres to StarRocks via Stream Load")
	root.AddCommand(newServeCommand(v))
	return root
}
