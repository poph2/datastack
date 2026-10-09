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
type Model struct {
	ID        uint64    `gorm:"primaryKey;index:,composite:cdc_cursor,priority:2"`
	CreatedAt time.Time `gorm:"<-:false;not null;default:now()"`
	UpdatedAt time.Time `gorm:"<-:false;not null;default:now();index:,composite:cdc_cursor,priority:1"`
	DeletedAt gorm.DeletedAt
}
