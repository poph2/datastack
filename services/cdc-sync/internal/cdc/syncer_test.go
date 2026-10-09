package cdc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/pgtest"
)

// widget is a source table shaped like every service's: database.Model gives
// it id, created_at, updated_at (set by the trigger) and deleted_at.
type widget struct {
	database.Model
	Name  string          `gorm:"size:50;not null"`
	Price decimal.Decimal `gorm:"type:decimal(10,2);not null"`
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fastSync syncs everything already committed, with no safety lag. Schema
// refreshes are forced explicitly by the tests that need them.
var fastSync = SyncConfig{PollInterval: time.Second, BatchSize: 1000, SafetyLag: 0, SchemaRefreshInterval: time.Hour}

// harness is one source database with a widgets table, a fake StarRocks and
// a single table loop driven tick by tick.
type harness struct {
	db       *gorm.DB
	starrock *fakeStarRocks
	sources  *Sources
	tracker  *Tracker
	ref      TableRef
}

// newHarness creates a fresh database named dbName on the shared Postgres,
// so cases don't see each other's rows.
func newHarness(t *testing.T, serverURL, dbName string) *harness {
	t.Helper()
	ctx := context.Background()

	admin, err := database.Open(ctx, serverURL)
	require.NoError(t, err)
	require.NoError(t, admin.Exec("CREATE DATABASE "+dbName).Error)
	require.NoError(t, database.Close(admin))

	db, err := database.Open(ctx, withDatabase(t, serverURL, dbName))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })
	require.NoError(t, database.AutoMigrate(ctx, db, &widget{}))

	sources, err := NewSources(serverURL)
	require.NoError(t, err)
	t.Cleanup(sources.Close)

	ref := TableRef{Database: dbName, Table: "widgets"}
	return &harness{db: db, starrock: newFakeStarRocks(t), sources: sources, tracker: NewTracker([]TableRef{ref}), ref: ref}
}

// syncer returns a table loop as the daemon would start it.
func (h *harness) syncer(t *testing.T, config SyncConfig) *tableSyncer {
	t.Helper()
	s, err := NewSyncer([]TableRef{h.ref}, Dependencies{
		Sources: h.sources,
		Catalog: h.starrock,
		Loader:  h.starrock.loader(),
		Tracker: h.tracker,
		Logger:  discardLogger,
	}, config)
	require.NoError(t, err)
	return s.tables[0]
}

// insertWidgets inserts n rows in one statement, so they share one NOW() and
// therefore one updated_at.
func (h *harness) insertWidgets(t *testing.T, n int) []widget {
	t.Helper()
	widgets := make([]widget, n)
	for i := range widgets {
		widgets[i] = widget{Name: fmt.Sprintf("w%d", i), Price: decimal.RequireFromString("19.99")}
	}
	require.NoError(t, h.db.Create(&widgets).Error)
	return widgets
}

func (h *harness) status() TableStatus {
	return h.tracker.Snapshot()[0]
}

// comment runs COMMENT ON statements against the source, as GORM
// AutoMigrate does for documented models.
func (h *harness) comment(t *testing.T, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		require.NoError(t, h.db.Exec(statement).Error)
	}
}

// refreshNow makes the next tick re-read the source schema.
func refreshNow(s *tableSyncer) {
	s.nextSchemaRefresh = time.Time{}
}

func withDatabase(t *testing.T, serverURL, dbName string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	require.NoError(t, err)
	u.Path = "/" + dbName
	return u.String()
}

func TestSync(t *testing.T) {
	serverURL := pgtest.Start(t)
	ctx := context.Background()

	t.Run("delivers every row once caught up", func(t *testing.T) {
		h := newHarness(t, serverURL, "caught_up")
		widgets := h.insertWidgets(t, 5)
		s := h.syncer(t, fastSync)

		s.tick(ctx)
		s.tick(ctx)

		rows, deliveries, _ := h.starrock.stats()
		assert.Equal(t, 5, rows)
		assert.Equal(t, 5, deliveries, "a caught-up table sends nothing more")

		got := h.starrock.row(widgets[0].ID)
		assert.Equal(t, "19.99", got["price"], "numerics travel as exact strings")
		assert.Nil(t, got["deleted_at"])

		status := h.status()
		assert.EqualValues(t, 5, status.RowsSynced)
		assert.EqualValues(t, widgets[4].ID, status.CursorID)
		assert.Empty(t, status.LastError)
		assert.NotNil(t, status.LastSuccessAt)
		assert.Zero(t, status.LagSeconds)
	})

	t.Run("reflects the source schema", func(t *testing.T) {
		h := newHarness(t, serverURL, "reflect")
		h.syncer(t, fastSync).tick(ctx)

		types := map[string]string{}
		for _, c := range h.starrock.columns {
			types[c.Name] = starRocksType(c)
		}
		assert.Equal(t, map[string]string{
			"id": "BIGINT", "created_at": "DATETIME", "updated_at": "DATETIME", "deleted_at": "DATETIME",
			"name": "VARCHAR(200)", "price": "DECIMAL(10,2)",
		}, types)
		assert.Equal(t, "id", h.starrock.columns[0].Name, "columns come in ordinal order")
	})

	t.Run("rows sharing updated_at across a batch boundary are not skipped", func(t *testing.T) {
		h := newHarness(t, serverURL, "same_ts")
		h.insertWidgets(t, 5)
		var distinct int
		require.NoError(t, h.db.Raw("SELECT COUNT(DISTINCT updated_at) FROM widgets").Scan(&distinct).Error)
		require.Equal(t, 1, distinct, "precondition: every row has the same updated_at")

		h.syncer(t, SyncConfig{PollInterval: time.Second, BatchSize: 2, SchemaRefreshInterval: time.Hour}).tick(ctx)

		rows, deliveries, labels := h.starrock.stats()
		assert.Equal(t, 5, rows)
		assert.Equal(t, 5, deliveries)
		assert.Len(t, labels, 3, "full batches drain immediately: 2 + 2 + 1")
	})

	t.Run("soft deletes are delivered", func(t *testing.T) {
		h := newHarness(t, serverURL, "soft_delete")
		widgets := h.insertWidgets(t, 2)
		s := h.syncer(t, fastSync)
		s.tick(ctx)

		require.NoError(t, h.db.Delete(&widgets[0]).Error)
		s.tick(ctx)

		assert.NotNil(t, h.starrock.row(widgets[0].ID)["deleted_at"])
		assert.Nil(t, h.starrock.row(widgets[1].ID)["deleted_at"])
	})

	t.Run("a failed load keeps the cursor and is retried", func(t *testing.T) {
		h := newHarness(t, serverURL, "failed_load")
		h.insertWidgets(t, 3)
		s := h.syncer(t, fastSync)
		h.starrock.failNextLoads(1)

		s.tick(ctx)

		rows, _, labels := h.starrock.stats()
		assert.Zero(t, rows)
		assert.Equal(t, epoch, s.cursor.UpdatedAt, "cursor must not move past an unloaded batch")
		assert.Contains(t, h.status().LastError, "injected failure")
		assert.Positive(t, h.status().LagSeconds)

		s.tick(ctx)

		rows, _, retryLabels := h.starrock.stats()
		assert.Equal(t, 3, rows)
		assert.Equal(t, labels[0], retryLabels[1], "the retry reuses the batch's label")
		assert.Empty(t, h.status().LastError)
	})

	t.Run("a batch that already landed counts as loaded", func(t *testing.T) {
		h := newHarness(t, serverURL, "already_loaded")
		h.insertWidgets(t, 2)
		s := h.syncer(t, fastSync)
		s.tick(ctx)

		// As if the process died after StarRocks committed but before the
		// cursor moved: the same batch is sent again under the same label.
		s.cursor = Cursor{UpdatedAt: epoch}
		s.tick(ctx)

		_, deliveries, labels := h.starrock.stats()
		assert.Len(t, labels, 2)
		assert.Equal(t, 2, deliveries, "StarRocks refused the duplicate")
		assert.EqualValues(t, h.status().CursorID, s.cursor.ID)
		assert.NotEqual(t, epoch, s.cursor.UpdatedAt, "the cursor moved on")
	})

	t.Run("safety lag holds back recent rows", func(t *testing.T) {
		h := newHarness(t, serverURL, "safety_lag")
		h.insertWidgets(t, 2)

		h.syncer(t, SyncConfig{PollInterval: time.Second, BatchSize: 1000, SafetyLag: time.Hour, SchemaRefreshInterval: time.Hour}).tick(ctx)

		rows, _, _ := h.starrock.stats()
		assert.Zero(t, rows, "rows changed within the safety lag wait for a later poll")
	})

	t.Run("restart resumes from what StarRocks holds", func(t *testing.T) {
		h := newHarness(t, serverURL, "restart")
		h.insertWidgets(t, 3)
		h.syncer(t, fastSync).tick(ctx)
		h.insertWidgets(t, 2)

		restarted := h.syncer(t, fastSync)
		restarted.tick(ctx)

		rows, deliveries, _ := h.starrock.stats()
		assert.Equal(t, 5, rows)
		// The first three share the newest loaded updated_at, so they are
		// re-sent (harmless upserts); nothing older is.
		assert.Equal(t, 3+3+2, deliveries)
	})

	t.Run("a missing source is retried until it appears", func(t *testing.T) {
		h := newHarness(t, serverURL, "late_source")
		require.NoError(t, h.db.Exec("DROP TABLE widgets").Error)
		s := h.syncer(t, fastSync)

		assert.NotPanics(t, func() { s.tick(ctx) })
		assert.False(t, s.ready)
		assert.Contains(t, h.status().LastError, "does not exist")

		require.NoError(t, database.AutoMigrate(ctx, h.db, &widget{}))
		h.insertWidgets(t, 1)
		s.tick(ctx)

		rows, _, _ := h.starrock.stats()
		assert.Equal(t, 1, rows)
	})

	t.Run("a missing database is retried, not fatal", func(t *testing.T) {
		h := newHarness(t, serverURL, "present")
		h.ref = TableRef{Database: "not_created_yet", Table: "widgets"}
		h.tracker = NewTracker([]TableRef{h.ref})
		s := h.syncer(t, fastSync)

		s.tick(ctx)

		assert.False(t, s.ready)
		assert.NotEmpty(t, h.status().LastError)
	})

	t.Run("a new source column is added before loading", func(t *testing.T) {
		h := newHarness(t, serverURL, "new_column")
		h.insertWidgets(t, 1)
		s := h.syncer(t, fastSync)
		s.tick(ctx)

		require.NoError(t, h.db.Exec("ALTER TABLE widgets ADD COLUMN colour varchar(10)").Error)
		require.NoError(t, h.db.Exec("UPDATE widgets SET colour = 'red'").Error)
		s.tick(ctx)

		assert.Empty(t, h.status().LastError)
		assert.Equal(t, "colour", h.starrock.columns[len(h.starrock.columns)-1].Name)
		assert.Equal(t, "red", h.starrock.row(1)["colour"])
	})

	t.Run("carries Postgres comments into the new table", func(t *testing.T) {
		h := newHarness(t, serverURL, "comments_create")
		h.comment(t,
			`COMMENT ON TABLE widgets IS 'Widgets for sale, one per SKU: "w" prefix'`,
			`COMMENT ON COLUMN widgets.id IS 'Widget id'`,
			`COMMENT ON COLUMN widgets.name IS 'Display name [ref: catalog.name]'`)

		h.syncer(t, fastSync).tick(ctx)

		statements := h.starrock.statements()
		require.Len(t, statements, 1, "a new table needs only its CREATE")
		assert.Contains(t, statements[0], "`id` BIGINT NOT NULL COMMENT 'Widget id'")
		assert.Contains(t, statements[0], "`name` VARCHAR(200) NULL COMMENT 'Display name [ref: catalog.name]'")
		assert.Contains(t, statements[0], "`price` DECIMAL(10,2) NULL\n)", "uncommented columns have no COMMENT")
		assert.Contains(t, statements[0], ") PRIMARY KEY (`id`)\nCOMMENT 'Widgets for sale, one per SKU: \"w\" prefix'\nDISTRIBUTED BY")
		assert.True(t, h.status().CommentsInSync)
	})

	t.Run("a changed comment is reconciled on the next schema refresh", func(t *testing.T) {
		h := newHarness(t, serverURL, "comments_change")
		h.comment(t, `COMMENT ON COLUMN widgets.name IS 'old'`)
		s := h.syncer(t, fastSync)
		s.tick(ctx)

		h.comment(t,
			`COMMENT ON TABLE widgets IS 'Widgets'`,
			`COMMENT ON COLUMN widgets.name IS 'Display name'`,
			`COMMENT ON COLUMN widgets.price IS 'Unit price, USD'`,
			`COMMENT ON COLUMN widgets.id IS 'Widget id'`)
		s.tick(ctx)
		assert.Len(t, h.starrock.statements(), 1, "nothing changes before the refresh is due")

		refreshNow(s)
		s.tick(ctx)

		assert.Equal(t, []string{
			"ALTER TABLE `analytics_db`.`widgets` COMMENT = 'Widgets'",
			"ALTER TABLE `analytics_db`.`widgets` " +
				"MODIFY COLUMN `name` VARCHAR(200) NULL COMMENT 'Display name', " +
				"MODIFY COLUMN `price` DECIMAL(10,2) NULL COMMENT 'Unit price, USD'",
		}, h.starrock.statements()[1:], "id's comment is left alone")
		assert.True(t, h.status().CommentsInSync)

		refreshNow(s)
		s.tick(ctx)
		assert.Len(t, h.starrock.statements(), 3, "comments in sync: nothing more to do")
	})

	t.Run("a schema change in progress never stops rows loading", func(t *testing.T) {
		h := newHarness(t, serverURL, "comments_busy")
		h.insertWidgets(t, 2)
		s := h.syncer(t, fastSync)
		s.tick(ctx)

		h.comment(t, `COMMENT ON COLUMN widgets.name IS 'Display name'`)
		h.starrock.setSchemaChangeRunning(true)
		h.insertWidgets(t, 3)
		refreshNow(s)
		s.tick(ctx)

		rows, _, _ := h.starrock.stats()
		assert.Equal(t, 5, rows, "rows load while the comment change waits")
		assert.Empty(t, h.status().LastError)
		assert.False(t, h.status().CommentsInSync)
		assert.Len(t, h.starrock.statements(), 1, "only the CREATE ran")

		h.starrock.setSchemaChangeRunning(false)
		refreshNow(s)
		s.tick(ctx)

		assert.Equal(t, "ALTER TABLE `analytics_db`.`widgets` MODIFY COLUMN `name` VARCHAR(200) NULL COMMENT 'Display name'",
			h.starrock.statements()[1], "retried on the next refresh")
		assert.True(t, h.status().CommentsInSync)
	})
}
