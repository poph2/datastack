// Package database opens the GORM connection every service uses for its own
// Postgres database.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/opentelemetry/tracing"
)

// Pool sizing for a small demo service. One Postgres instance hosts every
// service's database, so each pool is kept deliberately modest.
const (
	maxOpenConns    = 10
	maxIdleConns    = 5
	connMaxLifetime = 30 * time.Minute
	pingTimeout     = 5 * time.Second
)

// Open connects to Postgres, configures the pool, adds OpenTelemetry spans
// for every query, and checks the connection before returning.
func Open(ctx context.Context, databaseURL string) (*gorm.DB, error) {
	if databaseURL == "" {
		return nil, errors.New("database URL is empty (set --database-url or DATABASE_URL)")
	}

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		// Turns driver errors such as unique violations into gorm.ErrDuplicatedKey.
		TranslateError: true,
		Logger:         logger.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Metrics come from Prometheus. Query variables are left out so customer
	// data doesn't leak into traces.
	if err := db.Use(tracing.NewPlugin(tracing.WithoutMetrics(), tracing.WithoutQueryVariables())); err != nil {
		return nil, fmt.Errorf("installing tracing plugin: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("getting sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	if err := Ping(ctx, db); err != nil {
		return nil, err
	}
	return db, nil
}

// Ping checks that the database is reachable. Readiness probes use it.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("getting sql.DB: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("getting sql.DB: %w", err)
	}
	return sqlDB.Close()
}
