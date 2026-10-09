package shipment

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
	"github.com/poph2/datastack/pkgs/migrate"
	"github.com/poph2/datastack/pkgs/pgtest"
)

// neverDueMS is a packing delay no test waits out.
const neverDueMS = 3_600_000

// newTestRepository starts Postgres, runs the real migrate command, and
// returns a repository plus the raw DB for setup and assertions.
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

func newShipment(orderID uint64, packingDelayMS int) Shipment {
	return Shipment{
		OrderID:            orderID,
		Carrier:            CarrierUPS,
		TrackingNumber:     "1ZTEST",
		Status:             StatusPending,
		DestinationCountry: "US",
		PackingDelayMS:     packingDelayMS,
	}
}

func insertShipment(t *testing.T, repo *Repository, orderID uint64, packingDelayMS int) Shipment {
	t.Helper()
	s, created, err := repo.CreateOrGet(context.Background(), newShipment(orderID, packingDelayMS))
	require.NoError(t, err)
	require.True(t, created)
	return s
}

func shippedOrderIDs(shipments []Shipment) []uint64 {
	ids := make([]uint64, 0, len(shipments))
	for _, s := range shipments {
		ids = append(ids, s.OrderID)
	}
	return ids
}

func TestRepository(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("create returns the stored row", func(t *testing.T) {
		s, created, err := repo.CreateOrGet(ctx, newShipment(1, 2000))

		require.NoError(t, err)
		assert.True(t, created)
		assert.NotZero(t, s.ID)
		assert.False(t, s.CreatedAt.IsZero(), "created_at comes back from the database")
		assert.Equal(t, StatusPending, s.Status)
		assert.Nil(t, s.ShippedAt)

		got, err := repo.GetByOrderID(ctx, 1)
		require.NoError(t, err)
		assert.Equal(t, s.ID, got.ID)
		assert.Equal(t, 2000, got.PackingDelayMS)
	})

	t.Run("a second create for the same order returns the first", func(t *testing.T) {
		first := insertShipment(t, repo, 2, 2000)

		retry := newShipment(2, 9000)
		retry.Carrier = CarrierDHL
		got, created, err := repo.CreateOrGet(ctx, retry)

		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, first.ID, got.ID)
		assert.Equal(t, CarrierUPS, got.Carrier)
		assert.Equal(t, 2000, got.PackingDelayMS)

		var count int64
		require.NoError(t, db.Model(&Shipment{}).Where("order_id = 2").Count(&count).Error)
		assert.EqualValues(t, 1, count)
	})

	t.Run("concurrent creates for one order make one shipment", func(t *testing.T) {
		const callers = 10
		ids := make(chan uint64, callers)
		var wg sync.WaitGroup
		for range callers {
			wg.Go(func() {
				s, _, err := repo.CreateOrGet(ctx, newShipment(3, 2000))
				if assert.NoError(t, err) {
					ids <- s.ID
				}
			})
		}
		wg.Wait()
		close(ids)

		first := <-ids
		for id := range ids {
			assert.Equal(t, first, id)
		}
	})

	t.Run("unknown order", func(t *testing.T) {
		_, err := repo.GetByOrderID(ctx, 999_999)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("a soft-deleted shipment frees its order", func(t *testing.T) {
		s := insertShipment(t, repo, 4, 2000)
		require.NoError(t, db.Delete(&s).Error)

		_, err := repo.GetByOrderID(ctx, 4)
		assert.ErrorIs(t, err, ErrNotFound)
		insertShipment(t, repo, 4, 2000)
	})

	t.Run("check constraints reject bad values", func(t *testing.T) {
		badCarrier := newShipment(5, 2000)
		badCarrier.Carrier = "USPS"
		_, _, err := repo.CreateOrGet(ctx, badCarrier)
		assert.Error(t, err)

		_, _, err = repo.CreateOrGet(ctx, newShipment(6, -1))
		assert.Error(t, err)

		insertShipment(t, repo, 7, 2000)
		assert.Error(t, db.Exec("UPDATE shipments SET status = 'LOST' WHERE order_id = 7").Error)
	})
}

func TestShipDue(t *testing.T) {
	repo, db := newTestRepository(t)
	ctx := context.Background()

	t.Run("ships only rows whose packing delay has passed", func(t *testing.T) {
		insertShipment(t, repo, 1, 0)
		insertShipment(t, repo, 2, neverDueMS)
		// Created two seconds ago with a one-second delay: due by the
		// database clock, without the test sleeping.
		insertShipment(t, repo, 3, 1000)
		require.NoError(t, db.Exec("UPDATE shipments SET created_at = NOW() - INTERVAL '2 seconds' WHERE order_id = 3").Error)

		shipped, err := repo.ShipDue(ctx, 10)

		require.NoError(t, err)
		assert.ElementsMatch(t, []uint64{1, 3}, shippedOrderIDs(shipped))
		for _, s := range shipped {
			assert.Equal(t, StatusShipped, s.Status)
			require.NotNil(t, s.ShippedAt)
			assert.False(t, s.ShippedAt.Before(s.CreatedAt), "time-to-ship is never negative")
		}

		pending, err := repo.GetByOrderID(ctx, 2)
		require.NoError(t, err)
		assert.Equal(t, StatusPending, pending.Status)
		assert.Nil(t, pending.ShippedAt)

		shipped, err = repo.ShipDue(ctx, 10)
		require.NoError(t, err)
		assert.Empty(t, shipped, "a shipped row is never shipped again")
	})

	t.Run("respects the capacity, oldest first", func(t *testing.T) {
		for orderID := uint64(10); orderID < 15; orderID++ {
			insertShipment(t, repo, orderID, 0)
		}

		shipped, err := repo.ShipDue(ctx, 2)
		require.NoError(t, err)
		assert.Equal(t, []uint64{10, 11}, shippedOrderIDs(shipped))

		shipped, err = repo.ShipDue(ctx, 10)
		require.NoError(t, err)
		assert.ElementsMatch(t, []uint64{12, 13, 14}, shippedOrderIDs(shipped))
	})

	t.Run("skips soft-deleted rows", func(t *testing.T) {
		s := insertShipment(t, repo, 20, 0)
		require.NoError(t, db.Delete(&s).Error)

		shipped, err := repo.ShipDue(ctx, 10)
		require.NoError(t, err)
		assert.Empty(t, shipped)
	})

	t.Run("counts the pending backlog", func(t *testing.T) {
		insertShipment(t, repo, 30, neverDueMS)

		pending, err := repo.CountPending(ctx)
		require.NoError(t, err)
		assert.EqualValues(t, 2, pending, "order 2 and order 30")
	})
}

func TestShipDueConcurrentlyShipsEachRowOnce(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx := context.Background()

	const rows, replicas, capacity = 60, 8, 5
	for orderID := uint64(1); orderID <= rows; orderID++ {
		insertShipment(t, repo, orderID, 0)
	}

	// Each goroutine plays a replica's packer, running until it finds nothing
	// left. SKIP LOCKED means a replica may see nothing while another still
	// holds rows, but those rows are shipped by the replica holding them.
	results := make(chan []Shipment, rows)
	var wg sync.WaitGroup
	for range replicas {
		wg.Go(func() {
			for {
				shipped, err := repo.ShipDue(ctx, capacity)
				if !assert.NoError(t, err) || len(shipped) == 0 {
					return
				}
				assert.LessOrEqual(t, len(shipped), capacity)
				results <- shipped
			}
		})
	}
	wg.Wait()
	close(results)

	seen := map[uint64]bool{}
	total := 0
	for batch := range results {
		for _, s := range batch {
			assert.False(t, seen[s.ID], "shipment %d shipped twice", s.ID)
			seen[s.ID] = true
			total++
		}
	}
	assert.Equal(t, rows, total)

	pending, err := repo.CountPending(ctx)
	require.NoError(t, err)
	assert.Zero(t, pending)
}

func TestMigrateIsIdempotentAndBuildsIndexes(t *testing.T) {
	_, db := newTestRepository(t)

	// A second run, as on every redeploy, must change nothing and not fail.
	require.NoError(t, Migrate(context.Background(), db))

	indexDef := func(name string) string {
		var def string
		require.NoError(t, db.Raw("SELECT indexdef FROM pg_indexes WHERE tablename = 'shipments' AND indexname = ?", name).
			Scan(&def).Error)
		require.NotEmpty(t, def, "index %s missing", name)
		return def
	}

	t.Run("one live shipment per order", func(t *testing.T) {
		def := indexDef("idx_shipments_order_id")
		assert.Contains(t, def, "UNIQUE")
		assert.Contains(t, def, "(order_id)")
		assert.Contains(t, def, "WHERE (deleted_at IS NULL)")
	})

	t.Run("partial index for the packer", func(t *testing.T) {
		def := indexDef("idx_shipments_pending")
		assert.NotContains(t, def, "UNIQUE")
		assert.Contains(t, def, "(created_at)")
		assert.Contains(t, def, "'PENDING'")
		assert.Contains(t, def, "deleted_at IS NULL")
	})

	t.Run("cdc cursor index", func(t *testing.T) {
		assert.Contains(t, indexDef("idx_shipments_cdc_cursor"), "(updated_at, id)")
	})
}
