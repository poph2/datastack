package shipment

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ErrNotFound means no live shipment exists for the requested order.
var ErrNotFound = errors.New("shipment not found")

// shipDueSQL ships up to $1 due shipments, oldest first, and returns them.
//
// "Due" is evaluated entirely in SQL against NOW(), the same clock that set
// created_at, so pod clock skew can never ship a box early or late.
//
// FOR UPDATE SKIP LOCKED makes it safe for every replica to run this at once:
// a row another replica is shipping is skipped rather than waited for, and a
// row it has already shipped fails the status re-check, so each shipment is
// shipped exactly once. The selection is a MATERIALIZED CTE so it runs
// exactly once; an IN (subquery ... LIMIT) may be rescanned by the planner and
// then lock (and ship) more than $1 rows.
//
// It is raw SQL because GORM has no builder for an UPDATE driven by a locking
// CTE.
const shipDueSQL = `
WITH due AS MATERIALIZED (
    SELECT id
      FROM shipments
     WHERE status = 'PENDING'
       AND deleted_at IS NULL
       AND created_at + packing_delay_ms * INTERVAL '1 millisecond' <= NOW()
     ORDER BY created_at, id
     LIMIT ?
       FOR UPDATE SKIP LOCKED
)
UPDATE shipments
   SET status = 'SHIPPED', shipped_at = NOW()
  FROM due
 WHERE shipments.id = due.id
RETURNING shipments.*`

// Repository reads and writes shipments in the Shipment Service database.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateOrGet inserts s unless its order already has a live shipment, in
// which case it returns that one unchanged. created reports which happened.
//
// The unique index on order_id decides: when two requests for the same order
// race, one insert wins and the other loads the winner's row.
func (r *Repository) CreateOrGet(ctx context.Context, s Shipment) (shipment Shipment, created bool, err error) {
	err = r.db.WithContext(ctx).Create(&s).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		shipment, err = r.GetByOrderID(ctx, s.OrderID)
		return shipment, false, err
	}
	if err != nil {
		return Shipment{}, false, fmt.Errorf("creating shipment for order %d: %w", s.OrderID, err)
	}
	return s, true, nil
}

// GetByOrderID returns the live shipment for the given order.
func (r *Repository) GetByOrderID(ctx context.Context, orderID uint64) (Shipment, error) {
	var s Shipment
	err := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Shipment{}, ErrNotFound
	}
	if err != nil {
		return Shipment{}, fmt.Errorf("getting shipment for order %d: %w", orderID, err)
	}
	return s, nil
}

// ShipDue marks at most limit due shipments SHIPPED and returns them as they
// are afterwards. See shipDueSQL.
func (r *Repository) ShipDue(ctx context.Context, limit int) ([]Shipment, error) {
	var shipped []Shipment
	if err := r.db.WithContext(ctx).Raw(shipDueSQL, limit).Scan(&shipped).Error; err != nil {
		return nil, fmt.Errorf("shipping due shipments: %w", err)
	}
	return shipped, nil
}

// CountPending returns the backlog: live shipments not yet shipped.
func (r *Repository) CountPending(ctx context.Context) (int64, error) {
	var pending int64
	err := r.db.WithContext(ctx).Model(&Shipment{}).Where("status = ?", StatusPending).Count(&pending).Error
	if err != nil {
		return 0, fmt.Errorf("counting pending shipments: %w", err)
	}
	return pending, nil
}
