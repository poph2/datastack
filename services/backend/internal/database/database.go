package database
// Package database opens GORM connections that stamp every row with the simulated clock.
package database

import (
	"fmt"
	"os"

	"github.com/poph2/datastack/services/backend/internal/clock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const maxOpenConnections = 10

// Config holds the Postgres server connection settings shared by all microservices.
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
}

// ConfigFromEnv reads POSTGRES_* environment variables, falling back to local defaults.
func ConfigFromEnv() Config {
	return Config{
		Host:     envOr("POSTGRES_HOST", "localhost"),
		Port:     envOr("POSTGRES_PORT", "5432"),
		User:     envOr("POSTGRES_USER", "demo"),
		Password: envOr("POSTGRES_PASSWORD", "demo"),
	}
}

// Open connects to one database; created_at/updated_at/deleted_at all come from the simulated clock.
func Open(cfg Config, databaseName string) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, databaseName)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		NowFunc: clock.Now,
		Logger:  logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", databaseName, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("configuring pool for %s: %w", databaseName, err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConnections)

	return db, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
