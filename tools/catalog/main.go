// Command catalog validates the documented schema of every DataStack database
// and generates docs/microservices/data-catalog.md from it. Run it with
// `make catalog`, which migrates every service into the local Postgres first.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/poph2/datastack/tools/catalog/internal/catalog"
)

func main() {
	if err := newCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newCommand() *cobra.Command {
	var (
		postgresURL string
		databases   []string
		out         string
		check       bool
	)

	cmd := &cobra.Command{
		Use:          "catalog",
		Short:        "Validate schema documentation and generate the data catalog",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tables, err := readAll(cmd.Context(), postgresURL, databases)
			if err != nil {
				return err
			}
			if problems := catalog.Validate(tables); len(problems) > 0 {
				return fmt.Errorf("schema documentation problems:\n  %s", strings.Join(problems, "\n  "))
			}

			rendered := []byte(catalog.Render(tables))
			if check {
				return checkUpToDate(out, rendered)
			}
			if err := os.WriteFile(out, rendered, 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", out, err)
			}
			fmt.Printf("wrote %s (%d tables)\n", out, len(tables))
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&postgresURL, "postgres-url", "postgres://demo:demo@localhost:5433/?sslmode=disable",
		"Postgres URL without a database; each database name is set in turn")
	flags.StringSliceVar(&databases, "databases", []string{"customers", "marketing", "inventory", "payments", "shipments", "orders"},
		"databases to read, in catalog order")
	flags.StringVar(&out, "out", "docs/microservices/data-catalog.md", "catalog file to write (or check)")
	flags.BoolVar(&check, "check", false, "fail if the catalog file is out of date instead of writing it")
	return cmd
}

func readAll(ctx context.Context, postgresURL string, databases []string) ([]catalog.Table, error) {
	var all []catalog.Table
	for _, database := range databases {
		config, err := pgx.ParseConfig(postgresURL)
		if err != nil {
			return nil, fmt.Errorf("parsing postgres URL: %w", err)
		}
		config.Database = database

		conn, err := pgx.ConnectConfig(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("connecting to %s (did every service migrate? try `make catalog`): %w", database, err)
		}
		tables, err := catalog.Read(ctx, conn, database)
		conn.Close(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, tables...)
	}
	return all, nil
}

func checkUpToDate(path string, rendered []byte) error {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !bytes.Equal(current, rendered) {
		return fmt.Errorf("%s is out of date: run `make catalog`", path)
	}
	fmt.Printf("%s is up to date\n", path)
	return nil
}
