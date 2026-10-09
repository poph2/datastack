package payment

import (
	"context"
	"io"
	"log/slog"
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

// newTestRepository starts Postgres, runs the real migrate command, and
// returns a repository plus the raw DB for assertions the repository doesn't
// expose.
func newTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	url := pgtest.Start(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	require.NoError(t, migrate.Run(ctx, url, logger, Migrate))

	db, err := database.Open(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close(db) })

	return NewRepository(db), db
}

func newPayment(orderID uint64, status Status) *Payment {
	p := &Payment{
		OrderID: orderID,
		Amount:  decimal.RequireFromString("99.99"),
		Method:  MethodCreditCard,
		Status:  status,
	}
	if status == StatusDeclined {
		reason := ReasonCardDeclined
		p.DeclineReason = &reason
	}
	return p
}

func TestRepository(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("create then get", func(t *testing.T) {
		created := newPayment(1, StatusDeclined)
		require.NoError(t, repo.Create(ctx, created))
		require.NotZero(t, created.ID)
		assert.False(t, created.CreatedAt.IsZero())

		got, err := repo.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, uint64(1), got.OrderID)
		assert.Equal(t, "99.99", got.Amount.StringFixed(2))
		assert.Equal(t, StatusDeclined, got.Status)
		require.NotNil(t, got.DeclineReason)
		assert.Equal(t, ReasonCardDeclined, *got.DeclineReason)
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := repo.Get(ctx, 999_999)
		assert.ErrorIs(t, err, ErrNotFound)
		_, err = repo.Refund(ctx, 999_999)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("an order allows many declines but only one success", func(t *testing.T) {
		const orderID = 2
		require.NoError(t, repo.Create(ctx, newPayment(orderID, StatusDeclined)))
		require.NoError(t, repo.Create(ctx, newPayment(orderID, StatusDeclined)))
		require.NoError(t, repo.Create(ctx, newPayment(orderID, StatusSuccess)))

		assert.ErrorIs(t, repo.Create(ctx, newPayment(orderID, StatusSuccess)), ErrAlreadyPaid)
		assert.NoError(t, repo.Create(ctx, newPayment(orderID, StatusDeclined)),
			"a decline after payment is still recorded")
	})

	t.Run("concurrent charges never double-charge an order", func(t *testing.T) {
		const orderID, attempts = 3, 30

		var succeeded, alreadyPaid atomic.Int32
		var wg sync.WaitGroup
		for range attempts {
			wg.Go(func() {
				err := repo.Create(ctx, newPayment(orderID, StatusSuccess))
				switch {
				case err == nil:
					succeeded.Add(1)
				case assert.ErrorIs(t, err, ErrAlreadyPaid):
					alreadyPaid.Add(1)
				}
			})
		}
		wg.Wait()

		assert.EqualValues(t, 1, succeeded.Load())
		assert.EqualValues(t, attempts-1, alreadyPaid.Load())

		var stored int64
		require.NoError(t, db.Model(&Payment{}).
			Where("order_id = ? AND status = ?", orderID, StatusSuccess).Count(&stored).Error)
		assert.EqualValues(t, 1, stored)
	})

	t.Run("refund moves SUCCESS to REFUNDED and moves updated_at", func(t *testing.T) {
		p := newPayment(4, StatusSuccess)
		require.NoError(t, repo.Create(ctx, p))
		time.Sleep(10 * time.Millisecond)

		refunded, err := repo.Refund(ctx, p.ID)

		require.NoError(t, err)
		assert.Equal(t, p.ID, refunded.ID)
		assert.Equal(t, StatusRefunded, refunded.Status)
		assert.Equal(t, uint64(4), refunded.OrderID)
		assert.Equal(t, "99.99", refunded.Amount.StringFixed(2))
		assert.True(t, refunded.UpdatedAt.After(p.UpdatedAt), "a refund must move the CDC watermark")
	})

	t.Run("refund is guarded by status", func(t *testing.T) {
		success := newPayment(5, StatusSuccess)
		require.NoError(t, repo.Create(ctx, success))
		_, err := repo.Refund(ctx, success.ID)
		require.NoError(t, err)

		_, err = repo.Refund(ctx, success.ID)
		assert.ErrorIs(t, err, ErrNotRefundable, "a payment is refunded once")

		declined := newPayment(5, StatusDeclined)
		require.NoError(t, repo.Create(ctx, declined))
		_, err = repo.Refund(ctx, declined.ID)
		assert.ErrorIs(t, err, ErrNotRefundable)
	})

	t.Run("a refunded order can be charged again", func(t *testing.T) {
		first := newPayment(6, StatusSuccess)
		require.NoError(t, repo.Create(ctx, first))
		_, err := repo.Refund(ctx, first.ID)
		require.NoError(t, err)

		assert.NoError(t, repo.Create(ctx, newPayment(6, StatusSuccess)))
	})

	t.Run("check constraints", func(t *testing.T) {
		zeroAmount := newPayment(7, StatusSuccess)
		zeroAmount.Amount = decimal.Zero
		assert.Error(t, repo.Create(ctx, zeroAmount), "amount must be positive")

		badMethod := newPayment(7, StatusSuccess)
		badMethod.Method = "BITCOIN"
		assert.Error(t, repo.Create(ctx, badMethod))

		badStatus := newPayment(7, "PENDING")
		assert.Error(t, repo.Create(ctx, badStatus))

		declinedWithoutReason := newPayment(7, StatusDeclined)
		declinedWithoutReason.DeclineReason = nil
		assert.Error(t, repo.Create(ctx, declinedWithoutReason))

		successWithReason := newPayment(7, StatusSuccess)
		reason := ReasonFraudSuspected
		successWithReason.DeclineReason = &reason
		assert.Error(t, repo.Create(ctx, successWithReason))

		unknownReason := newPayment(7, StatusDeclined)
		bogus := DeclineReason("BAD_LUCK")
		unknownReason.DeclineReason = &bogus
		assert.Error(t, repo.Create(ctx, unknownReason))
	})
}

func TestDoubleChargeGuardIndex(t *testing.T) {
	// GORM's parsing of `where:` in index tags is subtle, so check what
	// Postgres actually built.
	_, db := newTestRepository(t)

	var def string
	require.NoError(t, db.Raw(
		"SELECT indexdef FROM pg_indexes WHERE tablename = 'payments' AND indexname = 'idx_payments_order_id_success'",
	).Scan(&def).Error)

	assert.Contains(t, def, "CREATE UNIQUE INDEX")
	assert.Contains(t, def, "(order_id)")
	assert.Contains(t, def, "WHERE")
	assert.Contains(t, def, "'SUCCESS'")
	assert.Contains(t, def, "deleted_at IS NULL")
}

func TestMigrateIsIdempotent(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, newPayment(1, StatusSuccess)))

	// A second run, as on every redeploy, must change nothing and not fail.
	require.NoError(t, Migrate(ctx, db))

	assert.ErrorIs(t, repo.Create(ctx, newPayment(1, StatusSuccess)), ErrAlreadyPaid,
		"the double-charge guard survives a re-migrate")
}

// The semantic layer (text-to-SQL) reads table and column comments from
// StarRocks, which copies them from Postgres. Every table and column must be
// documented, so a new field can't ship without a `comment:` tag.
func TestSchemaIsFullyDocumented(t *testing.T) {
	_, db := newTestRepository(t)

	missing, err := database.Undocumented(context.Background(), db)

	require.NoError(t, err)
	assert.Empty(t, missing, "add a comment: tag (columns) or TableComment (tables)")
}
