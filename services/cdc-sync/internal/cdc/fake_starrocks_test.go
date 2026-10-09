package cdc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStarRocks stands in for both sides of StarRocks: an httptest Stream
// Load endpoint that upserts rows by id like a Primary Key table, and the
// Catalog (DDL and starting cursor) answered from those same rows. The
// Catalog side plans its DDL with the real pure functions against the
// table's state, records the statements, and applies them to that state.
type fakeStarRocks struct {
	server *httptest.Server

	mu         sync.Mutex
	rows       map[string]map[string]any // by id, latest version wins
	labels     []string                  // every label attempted, failed ones too
	loaded     map[string]bool           // labels that committed
	deliveries int                       // rows received by successful loads
	failNext   int
	columns    []Column
	target     targetTable
	ddl        []string
	// schemaChangeRunning makes every comment ALTER fail the way StarRocks
	// refuses one while an earlier schema change job runs.
	schemaChangeRunning bool
}

// fakeDatabase is the StarRocks database the fake's DDL names.
const fakeDatabase = "analytics_db"

func newFakeStarRocks(t *testing.T) *fakeStarRocks {
	t.Helper()
	f := &fakeStarRocks{rows: map[string]map[string]any{}, loaded: map[string]bool{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.streamLoad))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeStarRocks) loader() *StreamLoader {
	return NewStreamLoader(StreamLoadConfig{URL: f.server.URL, Database: fakeDatabase, User: "root"})
}

func (f *fakeStarRocks) streamLoad(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	label := r.Header.Get("label")

	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels = append(f.labels, label)

	switch {
	case f.failNext > 0:
		f.failNext--
		writeJSON(w, map[string]string{"Status": "Fail", "Message": "injected failure"})
	case f.loaded[label]:
		writeJSON(w, map[string]string{"Status": "Label Already Exists", "ExistingJobStatus": "FINISHED"})
	default:
		if err := f.upsert(body); err != nil {
			writeJSON(w, map[string]string{"Status": "Fail", "Message": err.Error()})
			return
		}
		f.loaded[label] = true
		writeJSON(w, map[string]string{"Status": "Success"})
	}
}

func (f *fakeStarRocks) upsert(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var rows []map[string]any
	if err := decoder.Decode(&rows); err != nil {
		return err
	}
	for _, row := range rows {
		f.rows[fmt.Sprint(row["id"])] = row
	}
	f.deliveries += len(rows)
	return nil
}

func writeJSON(w http.ResponseWriter, body any) {
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeStarRocks) EnsureTable(_ context.Context, table string, schema TableSchema) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.columns = schema.Columns
	f.ddl = append(f.ddl, structureStatements(fakeDatabase, table, schema, f.target)...)
	if !f.target.exists() {
		f.target = targetTable{Comment: schema.Comment, Columns: map[string]string{}}
	}
	for _, c := range schema.Columns {
		if _, ok := f.target.Columns[strings.ToLower(c.Name)]; !ok {
			f.target.Columns[strings.ToLower(c.Name)] = c.Comment
		}
	}
	return nil
}

func (f *fakeStarRocks) SyncComments(_ context.Context, table string, schema TableSchema) (CommentChanges, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	changes := commentChanges(fakeDatabase, table, schema, f.target)
	if len(changes.Statements) == 0 {
		return changes, nil
	}
	if f.schemaChangeRunning {
		return changes, errors.New("Error 1064 (HY000): A schema change operation is in progress on the table " + table)
	}

	f.ddl = append(f.ddl, changes.Statements...)
	f.target.Comment = schema.Comment
	for _, c := range schema.Columns {
		name := strings.ToLower(c.Name)
		if _, ok := f.target.Columns[name]; ok && c.Name != columnID {
			f.target.Columns[name] = c.Comment
		}
	}
	return changes, nil
}

// statements returns every DDL statement run so far.
func (f *fakeStarRocks) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ddl...)
}

func (f *fakeStarRocks) setSchemaChangeRunning(running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.schemaChangeRunning = running
}

func (f *fakeStarRocks) MaxUpdatedAt(context.Context, string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var newest time.Time
	for _, row := range f.rows {
		ts, err := time.Parse(datetimeFormat, row[columnUpdatedAt].(string))
		if err != nil {
			return time.Time{}, err
		}
		if ts.After(newest) {
			newest = ts
		}
	}
	return newest, nil
}

func (f *fakeStarRocks) row(id uint64) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[fmt.Sprint(id)]
}

func (f *fakeStarRocks) stats() (rows, deliveries int, labels []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows), f.deliveries, append([]string(nil), f.labels...)
}

func (f *fakeStarRocks) failNextLoads(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = n
}
