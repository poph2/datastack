// Package shipment is the Shipment Service domain: assigning a carrier and
// tracking number to a paid order, and moving the box from PENDING (label
// printed) to SHIPPED (on the truck) after a simulated packing delay.
package shipment

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
)

// Carrier is the logistics company that moves the box.
type Carrier string

const (
	CarrierFedEx Carrier = "FEDEX"
	CarrierUPS   Carrier = "UPS"
	CarrierDHL   Carrier = "DHL"
)

// Status is the fulfillment state of a shipment.
type Status string

const (
	// StatusPending means the label is printed and the box is being packed.
	StatusPending Status = "PENDING"
	// StatusShipped means the box is on the truck. shipped_at is set.
	StatusShipped Status = "SHIPPED"
)

// Shipment is a row in the shipments table. The gorm tags are the schema:
// AutoMigrate creates the columns, CHECK constraints and indexes from them.
type Shipment struct {
	database.Model
	// One shipment per order, which is what makes create idempotent. Unique
	// among live rows only (see the soft-delete convention).
	OrderID        uint64  `gorm:"not null;uniqueIndex:,where:deleted_at IS NULL;comment:Order being shipped. At most one shipment per order. [ref: orders.id]"`
	Carrier        Carrier `gorm:"size:20;not null;check:chk_shipments_carrier,carrier IN ('FEDEX','UPS','DHL');comment:Shipping carrier: FEDEX, UPS or DHL. US destinations mostly use UPS and FedEx, other countries mostly DHL."`
	TrackingNumber string  `gorm:"size:100;not null;comment:Carrier tracking number, in the format of the carrier."`
	// rows and counts the backlog. It indexes created_at, which lives on the
	// embedded database.Model, so the tag sits here with an expression.
	Status             Status `gorm:"size:20;not null;check:chk_shipments_status,status IN ('PENDING','SHIPPED');index:idx_shipments_pending,expression:created_at,where:status = 'PENDING' AND deleted_at IS NULL;comment:PENDING (label printed, box being packed) or SHIPPED (handed to the carrier, shipped_at is set)."`
	DestinationCountry string `gorm:"size:2;not null;comment:Destination country as an ISO 3166-1 alpha-2 code, taken from the country of the customer at checkout."`
	// created_at + packing_delay_ms has passed, evaluated in SQL, so
	// Postgres stays the only clock.
	PackingDelayMS int `gorm:"not null;check:chk_shipments_packing_delay_ms,packing_delay_ms >= 0;comment:Simulated packing time assigned when the shipment was created, in milliseconds. GOLD customers get shorter delays. Actual time to ship can be longer when the warehouse is backlogged."`
	// Time-to-ship is shipped_at - created_at, never updated_at, which any
	// later state change would move.
	ShippedAt *time.Time `gorm:"comment:When the box left the warehouse, in UTC. NULL while PENDING. Time to ship is shipped_at minus created_at, never updated_at."`
}

// TableComment documents the table for the semantic layer (see
// database.TableCommenter).
func (Shipment) TableComment() string {
	return "Shipments (Shipment Service): one row per paid order handed to fulfillment, created right after payment succeeds. Join shipments.order_id to orders.id, one to one. Time to ship is shipped_at minus created_at. Rows with status PENDING are the fulfillment backlog."
}

// Migrate creates or updates the Shipment Service schema. There is no seed
// data: every shipment comes from a checkout.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return database.AutoMigrate(ctx, db, &Shipment{})
}
