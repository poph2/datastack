package customer

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/pkgs/pgtest"
	"github.com/poph2/datastack/services/customer/migrations"
)

// newTestRepository starts Postgres, applies the real migrations, and returns
// a repository plus the raw DB for assertions the repository doesn't expose.
func newTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	url := pgtest.Start(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	require.NoError(t, migrate.Up(ctx, url, migrations.FS, logger))

	db, err := database.Open(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	return NewRepository(db), db
}

func newCustomer(email string) *Customer {
	return &Customer{Email: email, Country: "US", LoyaltyTier: TierBronze, Status: StatusActive}
}

func TestRepository(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("create then get", func(t *testing.T) {
		created := newCustomer("create@example.com")
		require.NoError(t, repo.Create(ctx, created))
		require.NotZero(t, created.ID)

		got, err := repo.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "create@example.com", got.Email)
		assert.Equal(t, TierBronze, got.LoyaltyTier)
	})

	t.Run("duplicate email", func(t *testing.T) {
		require.NoError(t, repo.Create(ctx, newCustomer("dup@example.com")))
		assert.ErrorIs(t, repo.Create(ctx, newCustomer("dup@example.com")), ErrEmailExists)
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := repo.Get(ctx, 999_999)
		assert.ErrorIs(t, err, ErrNotFound)
		_, err = repo.Update(ctx, 999_999, Changes{Status: new(StatusSuspended)})
		assert.ErrorIs(t, err, ErrNotFound)
		assert.ErrorIs(t, repo.Delete(ctx, 999_999), ErrNotFound)
	})

	t.Run("update returns the new row and moves updated_at", func(t *testing.T) {
		c := newCustomer("update@example.com")
		require.NoError(t, repo.Create(ctx, c))

		updated, err := repo.Update(ctx, c.ID, Changes{Status: new(StatusSuspended)})
		require.NoError(t, err)
		assert.Equal(t, c.ID, updated.ID)
		assert.Equal(t, "update@example.com", updated.Email)
		assert.Equal(t, StatusSuspended, updated.Status)
		assert.Equal(t, TierBronze, updated.LoyaltyTier)
		assert.True(t, updated.UpdatedAt.After(c.UpdatedAt))
	})

	t.Run("soft delete hides the row but keeps it for CDC", func(t *testing.T) {
		c := newCustomer("delete@example.com")
		require.NoError(t, repo.Create(ctx, c))

		require.NoError(t, repo.Delete(ctx, c.ID))

		_, err := repo.Get(ctx, c.ID)
		assert.ErrorIs(t, err, ErrNotFound)

		var raw Customer
		require.NoError(t, db.Unscoped().First(&raw, c.ID).Error)
		assert.True(t, raw.DeletedAt.Valid)
		assert.True(t, raw.UpdatedAt.After(c.UpdatedAt), "soft delete must move the CDC watermark")
	})

	t.Run("a deleted customer's email can register again", func(t *testing.T) {
		first := newCustomer("again@example.com")
		require.NoError(t, repo.Create(ctx, first))
		require.NoError(t, repo.Delete(ctx, first.ID))

		assert.NoError(t, repo.Create(ctx, newCustomer("again@example.com")))
	})

	t.Run("trigger moves updated_at on raw SQL updates", func(t *testing.T) {
		c := newCustomer("trigger@example.com")
		require.NoError(t, repo.Create(ctx, c))
		time.Sleep(10 * time.Millisecond)

		require.NoError(t, db.Exec("UPDATE customers SET country = 'CA' WHERE id = ?", c.ID).Error)

		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.True(t, got.UpdatedAt.After(c.UpdatedAt))
	})

	t.Run("check constraints reject unknown enum values", func(t *testing.T) {
		bad := newCustomer("bad@example.com")
		bad.LoyaltyTier = "PLATINUM"
		assert.Error(t, repo.Create(ctx, bad))
	})
}

func TestMigrationsRollBackCleanly(t *testing.T) {
	ctx := context.Background()
	url := pgtest.Start(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	require.NoError(t, migrate.Up(ctx, url, migrations.FS, logger))
	require.NoError(t, migrate.Down(ctx, url, migrations.FS, logger))
	require.NoError(t, migrate.Down(ctx, url, migrations.FS, logger))
	require.NoError(t, migrate.Up(ctx, url, migrations.FS, logger))
}
