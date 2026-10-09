package database_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/pgtest"
)

type widget struct {
	database.Model
	Name string
}

func TestAutoMigrateBuildsTheBaseModelSchema(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, pgtest.Start(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	require.NoError(t, database.AutoMigrate(ctx, db, &widget{}))
	require.NoError(t, database.AutoMigrate(ctx, db, &widget{}), "AutoMigrate must be idempotent")

	t.Run("cdc cursor index on (updated_at, id)", func(t *testing.T) {
		var def string
		require.NoError(t, db.Raw("SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_widgets_cdc_cursor'").Scan(&def).Error)
		assert.Contains(t, def, "(updated_at, id)")
	})

	t.Run("updated_at trigger installed", func(t *testing.T) {
		var count int
		require.NoError(t, db.Raw("SELECT count(*) FROM pg_trigger WHERE tgname = 'widgets_set_updated_at'").Scan(&count).Error)
		assert.Equal(t, 1, count)
	})

	t.Run("timestamps come from the database clock", func(t *testing.T) {
		w := widget{Name: "a"}
		require.NoError(t, db.Create(&w).Error)
		assert.False(t, w.CreatedAt.IsZero(), "created_at is read back via RETURNING")
		assert.Equal(t, w.CreatedAt, w.UpdatedAt)

		time.Sleep(10 * time.Millisecond)
		require.NoError(t, db.Model(&w).Update("name", "b").Error)

		var reloaded widget
		require.NoError(t, db.First(&reloaded, w.ID).Error)
		assert.True(t, reloaded.UpdatedAt.After(w.UpdatedAt))
		assert.Equal(t, w.CreatedAt, reloaded.CreatedAt)
	})
}

func TestWithMigrationLockSerialisesRuns(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, pgtest.Start(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	// Two concurrent runs, as when two pods start together, must not overlap.
	// Atomics because the race detector can't see the Postgres lock.
	var active, maxActive atomic.Int32
	done := make(chan error, 2)
	for range 2 {
		go func() {
			done <- database.WithMigrationLock(ctx, db, func(conn *gorm.DB) error {
				if n := active.Add(1); n > maxActive.Load() {
					maxActive.Store(n)
				}
				time.Sleep(50 * time.Millisecond)
				active.Add(-1)
				return database.AutoMigrate(ctx, conn, &widget{})
			})
		}()
	}
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	assert.EqualValues(t, 1, maxActive.Load())
}

type documented struct {
	database.Model
	Name string `gorm:"comment:Display name, with quotes \"like this\" and [ref: widgets.id]"`
	Note string
}

func (documented) TableComment() string { return "One row per documented thing. Isn't it nice." }

type documentedV2 struct {
	database.Model
	Name string `gorm:"comment:Renamed meaning."`
	Note string `gorm:"comment:Now documented."`
}

func (documentedV2) TableName() string    { return "documenteds" }
func (documentedV2) TableComment() string { return "Second version." }

func TestCommentsReachPostgres(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, pgtest.Start(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	require.NoError(t, database.AutoMigrate(ctx, db, &documented{}))

	comment := func(sql string) string {
		var c string
		require.NoError(t, db.Raw(sql).Scan(&c).Error)
		return c
	}
	assert.Equal(t, "One row per documented thing. Isn't it nice.",
		comment("SELECT obj_description('documenteds'::regclass, 'pg_class')"), "quotes survive")
	assert.Equal(t, `Display name, with quotes "like this" and [ref: widgets.id]`,
		comment("SELECT col_description('documenteds'::regclass, 2 + 3)"), "column order: id, created_at, updated_at, deleted_at, name")
	assert.Contains(t, comment("SELECT col_description('documenteds'::regclass, 1)"), "primary key", "base Model columns are documented")

	missing, err := database.Undocumented(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, []string{"documenteds.note"}, missing)

	// Changing a comment and re-migrating updates it in place.
	require.NoError(t, database.AutoMigrate(ctx, db, &documentedV2{}))
	assert.Equal(t, "Second version.", comment("SELECT obj_description('documenteds'::regclass, 'pg_class')"))
	assert.Equal(t, "Renamed meaning.", comment("SELECT col_description('documenteds'::regclass, 5)"))
	missing, err = database.Undocumented(ctx, db)
	require.NoError(t, err)
	assert.Empty(t, missing)
}
