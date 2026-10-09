// Package migrate applies a service's embedded goose SQL migrations to its
// own database.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Up applies every pending migration in migrations (a filesystem whose root
// holds the NNNNN_name.sql files). A Postgres advisory lock serialises
// concurrent runs, so several replicas starting at once is safe.
func Up(ctx context.Context, databaseURL string, migrations fs.FS, logger *slog.Logger) error {
	return withProvider(databaseURL, migrations, logger, func(p *goose.Provider) error {
		results, err := p.Up(ctx)
		for _, r := range results {
			logger.Info("migration applied", "migration", r.Source.Path, "duration", r.Duration.String())
		}
		if err != nil {
			return fmt.Errorf("applying migrations: %w", err)
		}
		if len(results) == 0 {
			logger.Info("database schema is up to date")
		}
		return nil
	})
}

// Down rolls back the most recently applied migration.
func Down(ctx context.Context, databaseURL string, migrations fs.FS, logger *slog.Logger) error {
	return withProvider(databaseURL, migrations, logger, func(p *goose.Provider) error {
		result, err := p.Down(ctx)
		if err != nil {
			return fmt.Errorf("rolling back migration: %w", err)
		}
		logger.Info("migration rolled back", "migration", result.Source.Path, "duration", result.Duration.String())
		return nil
	})
}

// Status logs whether each migration is applied or pending.
func Status(ctx context.Context, databaseURL string, migrations fs.FS, logger *slog.Logger) error {
	return withProvider(databaseURL, migrations, logger, func(p *goose.Provider) error {
		statuses, err := p.Status(ctx)
		if err != nil {
			return fmt.Errorf("reading migration status: %w", err)
		}
		for _, s := range statuses {
			logger.Info("migration status", "migration", s.Source.Path, "state", s.State, "applied_at", s.AppliedAt)
		}
		return nil
	})
}

func withProvider(databaseURL string, migrations fs.FS, logger *slog.Logger, run func(*goose.Provider) error) error {
	if databaseURL == "" {
		return errors.New("database URL is empty (set --database-url or DATABASE_URL)")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("creating migration lock: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations,
		goose.WithSessionLocker(locker),
		goose.WithSlog(logger),
	)
	if err != nil {
		return fmt.Errorf("creating migration provider: %w", err)
	}
	defer provider.Close()

	return run(provider)
}
