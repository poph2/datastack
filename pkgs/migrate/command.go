// Package migrate provides the `migrate` command every service binary shares.
// Schemas come from GORM AutoMigrate on the service's models. There are no
// versioned migration files.
package migrate

import (
	"context"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/cli"
	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/logging"
)

// Func brings a service's database up to date: typically
// database.AutoMigrate on its models, plus any seed data.
type Func func(ctx context.Context, db *gorm.DB) error

// NewCommand returns the `migrate` command. In Kubernetes it runs as an
// initContainer before `serve`.
func NewCommand(serviceName string, v *viper.Viper, migrate Func) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Create or update this service's database schema and seed data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger, err := logging.New(serviceName, v.GetString(cli.FlagLogLevel), v.GetString(cli.FlagLogFormat))
			if err != nil {
				return err
			}
			return Run(cmd.Context(), v.GetString(cli.FlagDatabaseURL), logger, migrate)
		},
	}
}

// Run connects to the database and runs migrate under the migration lock, so
// several pods starting at once take turns.
func Run(ctx context.Context, databaseURL string, logger *slog.Logger, migrate Func) error {
	db, err := database.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Error("closing database", "error", err)
		}
	}()

	err = database.WithMigrationLock(ctx, db, func(conn *gorm.DB) error {
		return migrate(ctx, conn)
	})
	if err != nil {
		return err
	}

	logger.Info("database schema is up to date")
	return nil
}
