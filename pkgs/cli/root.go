// Package cli builds the cobra command tree every DataStack service binary shares.
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Shared flag names. Each flag can also be set through the environment
// variable of the same name, upper-cased with dashes turned into underscores
// (for example --database-url and DATABASE_URL).
const (
	FlagDatabaseURL = "database-url"
	FlagLogLevel    = "log-level"
	FlagLogFormat   = "log-format"
)

// NewRootCommand returns a root command with the flags every service needs,
// and the viper instance those flags (and their environment variables) are
// bound to. Services add their own subcommands, such as serve and migrate.
func NewRootCommand(use, short string) (*cobra.Command, *viper.Viper) {
	v := viper.New()

	root := &cobra.Command{
		Use:          use,
		Short:        short,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return bindFlagsAndEnv(v, cmd)
		},
	}

	flags := root.PersistentFlags()
	flags.String(FlagDatabaseURL, "", "Postgres connection URL for this service's database")
	flags.String(FlagLogLevel, "info", "log level: debug, info, warn, error")
	flags.String(FlagLogFormat, "json", "log format: json or text")

	return root, v
}

// bindFlagsAndEnv makes v resolve every flag of the executing command,
// preferring an explicitly set flag, then the environment, then the default.
func bindFlagsAndEnv(v *viper.Viper, cmd *cobra.Command) error {
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("binding flags: %w", err)
	}
	if err := v.BindPFlags(cmd.InheritedFlags()); err != nil {
		return fmt.Errorf("binding inherited flags: %w", err)
	}
	return nil
}
