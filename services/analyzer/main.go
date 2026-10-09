// Command analyzer runs the Analyzer Service: it answers natural-language
// business questions by generating SQL over StarRocks analytics_db.
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
const serviceName = "analyzer-service"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Kubernetes stops pods with SIGTERM. Cancelling the context drains the
	// HTTP server and stops the schema refresh.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCommand().ExecuteContext(ctx)
}

// newRootCommand has serve and eval. The service owns no Postgres database,
// so there is nothing to migrate.
func newRootCommand() *cobra.Command {
	root, v := cli.NewRootCommand("analyzer", "Analyzer Service: natural-language questions to StarRocks SQL")
	root.AddCommand(newServeCommand(v), newEvalCommand(v))
	return root
}
