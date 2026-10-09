package database

import (
	"time"

	"gorm.io/gorm"
)

// Model is the base every table embeds. The columns match the convention the
// CDC sync daemon relies on:
//
//   - id: auto-incrementing primary key.
//   - created_at / updated_at: set only by Postgres (column defaults plus the
//     set_updated_at trigger AutoMigrate installs) and read back via
//     RETURNING. GORM never writes them. If it did, inserts would use the
//     pod's clock and updates the database's clock, and any skew between them
//     could move updated_at backwards, so the sync daemon would skip rows.
//   - deleted_at: soft delete. GORM filters these rows out of queries, and
//     the trigger moves updated_at so the sync daemon sees the delete.
//
// The composite (updated_at, id) index, named idx_<table>_cdc_cursor, serves
// the sync daemon's cursor query: WHERE (updated_at, id) > ($1, $2).
//
// The comments are the shared part of every table's documentation (see
// TableCommenter): they travel to Postgres and on to StarRocks, where a
// text-to-SQL analyzer reads them.
type Model struct {
	ID        uint64         `gorm:"primaryKey;index:,composite:cdc_cursor,priority:2;comment:Surrogate primary key of this row, unique within this table."`
	CreatedAt time.Time      `gorm:"<-:false;not null;default:now();comment:When the row was created, in UTC, set by the database clock."`
	UpdatedAt time.Time      `gorm:"<-:false;not null;default:now();index:,composite:cdc_cursor,priority:1;comment:When the row last changed, in UTC, set by the database clock on every update. Not a business event time."`
	DeletedAt gorm.DeletedAt `gorm:"comment:When the row was soft-deleted, in UTC. NULL for live rows. Filter on deleted_at IS NULL unless analysing deletions."`
}
