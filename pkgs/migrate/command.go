package migrate

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/logging"
)

type action func(ctx context.Context, databaseURL string, migrations fs.FS, logger *slog.Logger) error

// NewCommand returns the `migrate` command with up, down and status
// subcommands, operating on the embedded migrations of the calling service.
// In Kubernetes, `migrate up` runs as an initContainer before `serve`.
func NewCommand(serviceName string, v *viper.Viper, migrations fs.FS) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Manage this service's database schema",
	}

	cmd.AddCommand(
		newSubcommand("up", "Apply all pending migrations", serviceName, v, migrations, Up),
		newSubcommand("down", "Roll back the most recent migration", serviceName, v, migrations, Down),
		newSubcommand("status", "Show applied and pending migrations", serviceName, v, migrations, Status),
	)
	return cmd
}

func newSubcommand(use, short, serviceName string, v *viper.Viper, migrations fs.FS, run action) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger, err := logging.New(serviceName, v.GetString(cli.FlagLogLevel), v.GetString(cli.FlagLogFormat))
			if err != nil {
				return err
			}
			return run(cmd.Context(), v.GetString(cli.FlagDatabaseURL), migrations, logger)
		},
	}
}
