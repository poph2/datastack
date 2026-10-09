package database

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// migrationLockKey is the Postgres advisory lock that serialises schema
// changes, so several pods starting at once don't run AutoMigrate together.
// Advisory locks are scoped to the current database, so every service can
// use the same key.
const migrationLockKey = 72_617_301

// setUpdatedAtFunction keeps Postgres the only clock for updated_at (see
// Model). AutoMigrate can't create triggers, so this runs after it.
const setUpdatedAtFunction = `
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql`

// AutoMigrate creates or updates the tables for models (including column
// comments from `comment:` tags), installs the set_updated_at trigger on each,
// and applies table comments (see TableCommenter). It's idempotent, so it is
// safe to run on every deploy.
func AutoMigrate(ctx context.Context, db *gorm.DB, models ...any) error {
	db = db.WithContext(ctx)

	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto-migrating: %w", err)
	}
	if err := db.Exec(setUpdatedAtFunction).Error; err != nil {
		return fmt.Errorf("creating set_updated_at function: %w", err)
	}

	for _, model := range models {
		table, err := tableName(db, model)
		if err != nil {
			return err
		}
		err = db.Exec("CREATE OR REPLACE TRIGGER ? BEFORE UPDATE ON ? FOR EACH ROW EXECUTE FUNCTION set_updated_at()",
			clause.Table{Name: table + "_set_updated_at"}, clause.Table{Name: table}).Error
		if err != nil {
			return fmt.Errorf("creating updated_at trigger on %s: %w", table, err)
		}
		if err := applyTableComment(db, model, table); err != nil {
			return err
		}
	}
	return nil
}

// WithMigrationLock runs fn on a single connection that holds the migration
// advisory lock. Every statement fn runs uses that connection.
func WithMigrationLock(ctx context.Context, db *gorm.DB, fn func(conn *gorm.DB) error) error {
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("SELECT pg_advisory_lock(?)", migrationLockKey).Error; err != nil {
			return fmt.Errorf("acquiring migration lock: %w", err)
		}
		defer conn.Exec("SELECT pg_advisory_unlock(?)", migrationLockKey)

		return fn(conn)
	})
}

func tableName(db *gorm.DB, model any) (string, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		return "", fmt.Errorf("parsing model %T: %w", model, err)
	}
	return stmt.Schema.Table, nil
}
