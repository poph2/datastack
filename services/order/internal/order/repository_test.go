package order

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/pkgs/pgtest"
)

func newTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	url := pgtest.Start(t)

	require.NoError(t, migrate.Run(ctx, url, discardLogger, Migrate))

	db, err := database.Open(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	return NewRepository(db), db
}

func newPendingOrder(t *testing.T, repo *Repository) Order {
	t.Helper()
	o := Order{UserID: 1, ProductID: "PROD_0001", Quantity: 2, Status: StatusPending}
	require.NoError(t, repo.Create(context.Background(), &o))
	return o
}

func TestRepository(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("create then get", func(t *testing.T) {
		o := newPendingOrder(t, repo)

		got, err := repo.Get(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, StatusPending, got.Status)
		assert.Nil(t, got.UnitPrice)
		assert.Nil(t, got.AttributionID)

		_, err = repo.Get(ctx, 999_999)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("apply sets prices and moves status", func(t *testing.T) {
		o := newPendingOrder(t, repo)
		price, total := decimal.RequireFromString("10.50"), decimal.RequireFromString("21.00")

		priced, err := repo.Apply(ctx, o.ID, Transition{From: StatusPending, To: StatusPending, UnitPrice: &price, TotalAmount: &total})
		require.NoError(t, err)
		assert.Equal(t, "21.00", priced.TotalAmount.StringFixed(2))

		paid, err := repo.Apply(ctx, o.ID, Transition{From: StatusPending, To: StatusPaid})
		require.NoError(t, err)
		assert.Equal(t, StatusPaid, paid.Status)
		assert.Equal(t, "10.50", paid.UnitPrice.StringFixed(2), "unchanged fields are kept")
		assert.True(t, paid.UpdatedAt.After(o.UpdatedAt))
	})

	t.Run("apply is guarded by the current status", func(t *testing.T) {
		o := newPendingOrder(t, repo)
		_, err := repo.Apply(ctx, o.ID, Transition{From: StatusPaid, To: StatusShipped})
		assert.ErrorIs(t, err, ErrStatusChanged)

		_, err = repo.Apply(ctx, 999_999, Transition{From: StatusPending, To: StatusPaid})
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("concurrent transitions: exactly one wins", func(t *testing.T) {
		o := newPendingOrder(t, repo)
		_, err := repo.Apply(ctx, o.ID, Transition{From: StatusPending, To: StatusPaid})
		require.NoError(t, err)

		var won atomic.Int32
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				if _, err := repo.Apply(ctx, o.ID, Transition{From: StatusPaid, To: StatusShipped}); err == nil {
					won.Add(1)
				}
			})
		}
		wg.Wait()
		assert.EqualValues(t, 1, won.Load())
	})

	t.Run("failure reason is stored and constrained", func(t *testing.T) {
		o := newPendingOrder(t, repo)
		reason := ReasonOutOfStock
		failed, err := repo.Apply(ctx, o.ID, Transition{From: StatusPending, To: StatusFailed, FailureReason: &reason})
		require.NoError(t, err)
		assert.Equal(t, ReasonOutOfStock, *failed.FailureReason)

		bad := FailureReason("BAD_LUCK")
		other := newPendingOrder(t, repo)
		_, err = repo.Apply(ctx, other.ID, Transition{From: StatusPending, To: StatusFailed, FailureReason: &bad})
		assert.Error(t, err)
	})

	t.Run("stuck paid uses the database clock", func(t *testing.T) {
		require.NoError(t, db.Exec("UPDATE orders SET status = 'FAILED', failure_reason = 'UPSTREAM_ERROR' WHERE status = 'PAID'").Error)
		fresh := newPendingOrder(t, repo)
		_, err := repo.Apply(ctx, fresh.ID, Transition{From: StatusPending, To: StatusPaid})
		require.NoError(t, err)

		stuck := newPendingOrder(t, repo)
		_, err = repo.Apply(ctx, stuck.ID, Transition{From: StatusPending, To: StatusPaid})
		require.NoError(t, err)
		// Backdate with the trigger off, since the trigger would reset updated_at.
		require.NoError(t, db.Exec("ALTER TABLE orders DISABLE TRIGGER orders_set_updated_at").Error)
		require.NoError(t, db.Exec("UPDATE orders SET updated_at = NOW() - interval '5 minutes' WHERE id = ?", stuck.ID).Error)
		require.NoError(t, db.Exec("ALTER TABLE orders ENABLE TRIGGER orders_set_updated_at").Error)

		orders, err := repo.StuckPaid(ctx, time.Minute, 10)
		require.NoError(t, err)
		require.Len(t, orders, 1)
		assert.Equal(t, stuck.ID, orders[0].ID)
	})
}

func TestMigrateIsIdempotent(t *testing.T) {
	_, db := newTestRepository(t)
	require.NoError(t, Migrate(context.Background(), db))
}
