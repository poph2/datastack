package marketing

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

// newTestRepository starts Postgres, runs the real migrate command (schema
// plus seed campaigns), and returns a repository plus the raw DB for
// assertions the repository doesn't expose.
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

// newAttribution logs a facebook/summer_sale touchpoint (a seeded campaign).
func newAttribution(t *testing.T, repo *Repository) Attribution {
	t.Helper()
	a := Attribution{UserID: 1234, UTMSource: "facebook", UTMCampaign: "summer_sale"}
	require.NoError(t, repo.CreateAttribution(context.Background(), &a))
	return a
}

func conversion(orderID uint64, amount string) Conversion {
	return Conversion{OrderID: orderID, OrderAmount: decimal.RequireFromString(amount)}
}

func TestSeedCampaigns(t *testing.T) {
	repo, db := newTestRepository(t)

	campaigns, err := repo.ListCampaigns(context.Background())
	require.NoError(t, err)

	want := []struct{ source, campaign, adSpend string }{
		{"google", "search_brand", "1200.00"},
		{"facebook", "summer_sale", "800.00"},
		{"instagram", "influencer_q3", "650.00"},
		{"tiktok", "viral_launch", "400.00"},
		{"email", "newsletter", "0.00"},
		{"organic", "none", "0.00"},
	}
	require.Len(t, campaigns, len(want))
	for i, w := range want {
		assert.Equal(t, uint64(i+1), campaigns[i].ID, "seed IDs are deterministic")
		assert.Equal(t, w.source, campaigns[i].UTMSource)
		assert.Equal(t, w.campaign, campaigns[i].UTMCampaign)
		assert.Equal(t, w.adSpend, campaigns[i].AdSpend.StringFixed(2))
	}

	// Guards the GORM zero-value trap: a column default would replace the
	// zero spend on insert.
	var emailSpend string
	require.NoError(t, db.Raw("SELECT ad_spend::text FROM campaigns WHERE utm_source = 'email'").Scan(&emailSpend).Error)
	assert.Equal(t, "0.00", emailSpend)
}

func TestRepositoryCreateAttribution(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx := context.Background()

	t.Run("known campaign", func(t *testing.T) {
		a := newAttribution(t, repo)
		assert.NotZero(t, a.ID)
		assert.False(t, a.CreatedAt.IsZero(), "created_at is read back from the database")

		got, err := repo.GetAttribution(ctx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, uint64(1234), got.UserID)
		assert.False(t, got.Converted())
		assert.Nil(t, got.OrderAmount)
		assert.Nil(t, got.ConvertedAt)
	})

	t.Run("unknown campaign", func(t *testing.T) {
		for _, pair := range [][2]string{{"facebook", "nope"}, {"myspace", "summer_sale"}} {
			a := Attribution{UserID: 1, UTMSource: pair[0], UTMCampaign: pair[1]}
			assert.ErrorIs(t, repo.CreateAttribution(ctx, &a), ErrCampaignNotFound, pair)
		}
	})

	t.Run("soft-deleted campaign takes no new attributions", func(t *testing.T) {
		c := Campaign{UTMSource: "retired", UTMCampaign: "spring", AdSpend: decimal.NewFromInt(5)}
		require.NoError(t, repo.db.Create(&c).Error)
		require.NoError(t, repo.db.Delete(&c).Error)

		a := Attribution{UserID: 1, UTMSource: "retired", UTMCampaign: "spring"}
		assert.ErrorIs(t, repo.CreateAttribution(ctx, &a), ErrCampaignNotFound)

		// The partial unique index lets the pair be created again.
		again := Campaign{UTMSource: "retired", UTMCampaign: "spring", AdSpend: decimal.NewFromInt(5)}
		assert.NoError(t, repo.db.Create(&again).Error)
	})

	t.Run("unknown attribution", func(t *testing.T) {
		_, err := repo.GetAttribution(ctx, 999_999)
		assert.ErrorIs(t, err, ErrAttributionNotFound)
	})
}

func TestRepositoryConvert(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("first conversion sets all three fields from the database clock", func(t *testing.T) {
		a := newAttribution(t, repo)

		result, err := repo.Convert(ctx, a.ID, conversion(999, "150.00"))

		require.NoError(t, err)
		assert.True(t, result.FirstConversion)
		got := result.Attribution
		assert.Equal(t, a.ID, got.ID)
		assert.Equal(t, "facebook", got.UTMSource, "RETURNING fills the whole row")
		require.NotNil(t, got.OrderID)
		assert.Equal(t, uint64(999), *got.OrderID)
		assert.Equal(t, "150.00", got.OrderAmount.StringFixed(2))
		require.NotNil(t, got.ConvertedAt)
		// NOW() and the updated_at trigger both read the transaction's clock,
		// so equality proves converted_at came from Postgres.
		assert.Equal(t, got.UpdatedAt, *got.ConvertedAt)
		assert.True(t, got.ConvertedAt.After(a.CreatedAt))
	})

	t.Run("same order again is idempotent and keeps the first values", func(t *testing.T) {
		a := newAttribution(t, repo)
		first, err := repo.Convert(ctx, a.ID, conversion(1000, "20.00"))
		require.NoError(t, err)

		again, err := repo.Convert(ctx, a.ID, conversion(1000, "25.00"))

		require.NoError(t, err)
		assert.False(t, again.FirstConversion)
		assert.Equal(t, "20.00", again.Attribution.OrderAmount.StringFixed(2))
		assert.Equal(t, *first.Attribution.ConvertedAt, *again.Attribution.ConvertedAt)
		assert.Equal(t, first.Attribution.UpdatedAt, again.Attribution.UpdatedAt, "a retry writes nothing, so CDC sees no change")
	})

	t.Run("a different order conflicts", func(t *testing.T) {
		a := newAttribution(t, repo)
		_, err := repo.Convert(ctx, a.ID, conversion(1001, "20.00"))
		require.NoError(t, err)

		_, err = repo.Convert(ctx, a.ID, conversion(1002, "20.00"))
		assert.ErrorIs(t, err, ErrAlreadyConverted)

		got, err := repo.GetAttribution(ctx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, uint64(1001), *got.OrderID)
	})

	t.Run("unknown or soft-deleted attribution", func(t *testing.T) {
		_, err := repo.Convert(ctx, 999_999, conversion(1, "1.00"))
		assert.ErrorIs(t, err, ErrAttributionNotFound)

		a := newAttribution(t, repo)
		require.NoError(t, db.Delete(&a).Error)
		_, err = repo.Convert(ctx, a.ID, conversion(1, "1.00"))
		assert.ErrorIs(t, err, ErrAttributionNotFound)
	})

	t.Run("check constraint keeps the conversion fields together", func(t *testing.T) {
		a := newAttribution(t, repo)
		err := db.Exec("UPDATE attributions SET order_id = 5 WHERE id = ?", a.ID).Error
		assert.Error(t, err)
	})
}

func TestConcurrentConversionsOfDifferentOrdersHaveOneWinner(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx := context.Background()
	a := newAttribution(t, repo)

	const orders = 30
	var won, conflicted atomic.Int32
	var wg sync.WaitGroup
	for i := range orders {
		wg.Go(func() {
			result, err := repo.Convert(ctx, a.ID, conversion(uint64(5000+i), "10.00"))
			switch {
			case err == nil:
				assert.True(t, result.FirstConversion)
				won.Add(1)
			case assert.ErrorIs(t, err, ErrAlreadyConverted):
				conflicted.Add(1)
			}
		})
	}
	wg.Wait()

	assert.EqualValues(t, 1, won.Load())
	assert.EqualValues(t, orders-1, conflicted.Load())
}

func TestConcurrentRetriesOfTheSameOrderConvertOnce(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx := context.Background()
	a := newAttribution(t, repo)

	const retries = 30
	var first, idempotent atomic.Int32
	var wg sync.WaitGroup
	for range retries {
		wg.Go(func() {
			result, err := repo.Convert(ctx, a.ID, conversion(7000, "10.00"))
			if !assert.NoError(t, err) {
				return
			}
			if result.FirstConversion {
				first.Add(1)
			} else {
				idempotent.Add(1)
			}
		})
	}
	wg.Wait()

	assert.EqualValues(t, 1, first.Load(), "the conversion metric must count exactly once")
	assert.EqualValues(t, retries-1, idempotent.Load())
}

func TestSchema(t *testing.T) {
	_, db := newTestRepository(t)

	indexDef := func(name string) string {
		var def string
		require.NoError(t, db.Raw("SELECT indexdef FROM pg_indexes WHERE indexname = ?", name).Scan(&def).Error)
		return def
	}

	t.Run("campaign pair is unique among live rows", func(t *testing.T) {
		def := indexDef("idx_campaigns_source_campaign")
		assert.Contains(t, def, "CREATE UNIQUE INDEX")
		assert.Contains(t, def, "(utm_source, utm_campaign)")
		assert.Contains(t, def, "WHERE (deleted_at IS NULL)")

		dup := Campaign{UTMSource: "google", UTMCampaign: "search_brand", AdSpend: decimal.NewFromInt(1)}
		assert.ErrorIs(t, db.Create(&dup).Error, gorm.ErrDuplicatedKey)
	})

	t.Run("cdc cursor index on both tables", func(t *testing.T) {
		assert.Contains(t, indexDef("idx_campaigns_cdc_cursor"), "(updated_at, id)")
		assert.Contains(t, indexDef("idx_attributions_cdc_cursor"), "(updated_at, id)")
	})

	t.Run("updated_at trigger on both tables", func(t *testing.T) {
		var count int
		require.NoError(t, db.Raw(
			"SELECT count(*) FROM pg_trigger WHERE tgname IN ('campaigns_set_updated_at', 'attributions_set_updated_at')",
		).Scan(&count).Error)
		assert.Equal(t, 2, count)
	})

	t.Run("ad spend can't go negative", func(t *testing.T) {
		assert.Error(t, db.Exec("UPDATE campaigns SET ad_spend = -1 WHERE utm_source = 'google'").Error)
	})

	t.Run("trigger moves updated_at on raw SQL updates", func(t *testing.T) {
		var before time.Time
		require.NoError(t, db.Raw("SELECT updated_at FROM campaigns WHERE id = 1").Scan(&before).Error)
		time.Sleep(10 * time.Millisecond)

		require.NoError(t, db.Exec("UPDATE campaigns SET ad_spend = ad_spend + 1 WHERE id = 1").Error)

		var after time.Time
		require.NoError(t, db.Raw("SELECT updated_at FROM campaigns WHERE id = 1").Scan(&after).Error)
		assert.True(t, after.After(before))
	})
}

func TestMigrateIsIdempotent(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()
	require.NoError(t, db.Exec("UPDATE campaigns SET ad_spend = 1500 WHERE utm_source = 'google'").Error)

	// A second run, as on every redeploy, must neither fail nor re-seed or
	// reset ad spend.
	require.NoError(t, Migrate(ctx, db))

	campaigns, err := repo.ListCampaigns(ctx)
	require.NoError(t, err)
	assert.Len(t, campaigns, len(seedCatalog))
	assert.Equal(t, "1500.00", campaigns[0].AdSpend.StringFixed(2))
}
