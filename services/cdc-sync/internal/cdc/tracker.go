package cdc

import (
	"sync"
	"time"
)

// TableStatus is one table's sync progress, as served by GET /status.
type TableStatus struct {
	Table           string     `json:"table"`
	CursorUpdatedAt time.Time  `json:"cursor_updated_at"`
	CursorID        int64      `json:"cursor_id"`
	RowsSynced      int64      `json:"rows_synced"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	LastError       string     `json:"last_error"`
	LagSeconds      float64    `json:"lag_seconds"`
	// CommentsInSync is true once the StarRocks table and column comments
	// were last found (or made) equal to Postgres'. The id comment is never
	// changed, so it doesn't count.
	CommentsInSync bool `json:"comments_in_sync"`
}

// Tracker records every table loop's progress. The loops write to it and the
// /status handler reads it concurrently.
type Tracker struct {
	mu     sync.Mutex
	order  []string
	tables map[string]*TableStatus
}

// NewTracker returns a Tracker listing refs in the given order.
func NewTracker(refs []TableRef) *Tracker {
	t := &Tracker{tables: map[string]*TableStatus{}}
	for _, ref := range refs {
		name := ref.String()
		t.order = append(t.order, name)
		t.tables[name] = &TableStatus{Table: name}
	}
	return t
}

// Snapshot returns a copy of every table's status, in configuration order.
func (t *Tracker) Snapshot() []TableStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	snapshot := make([]TableStatus, 0, len(t.order))
	for _, name := range t.order {
		snapshot = append(snapshot, *t.tables[name])
	}
	return snapshot
}

func (t *Tracker) setCursor(ref TableRef, cursor Cursor) {
	t.update(ref, func(s *TableStatus) {
		s.CursorUpdatedAt, s.CursorID = cursor.UpdatedAt, cursor.ID
	})
}

func (t *Tracker) recordLoad(ref TableRef, cursor Cursor, rows int, at time.Time) {
	t.update(ref, func(s *TableStatus) {
		s.CursorUpdatedAt, s.CursorID = cursor.UpdatedAt, cursor.ID
		s.RowsSynced += int64(rows)
		s.LastSuccessAt = &at
		s.LastError = ""
	})
}

func (t *Tracker) recordError(ref TableRef, err error) {
	t.update(ref, func(s *TableStatus) { s.LastError = err.Error() })
}

func (t *Tracker) clearError(ref TableRef) {
	t.update(ref, func(s *TableStatus) { s.LastError = "" })
}

func (t *Tracker) setCommentsInSync(ref TableRef, inSync bool) {
	t.update(ref, func(s *TableStatus) { s.CommentsInSync = inSync })
}

func (t *Tracker) setLag(ref TableRef, lag time.Duration) {
	t.update(ref, func(s *TableStatus) { s.LagSeconds = lag.Seconds() })
}

func (t *Tracker) update(ref TableRef, apply func(*TableStatus)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if status, ok := t.tables[ref.String()]; ok {
		apply(status)
	}
}
