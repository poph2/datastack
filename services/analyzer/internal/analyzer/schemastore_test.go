package analyzer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSchemaSource counts grants and returns a scripted schema or error.
type fakeSchemaSource struct {
	grants    int
	grantErr  error
	loadErr   error
	tableName string
}

func (s *fakeSchemaSource) EnsureReadOnlyUser(context.Context) error {
	s.grants++
	return s.grantErr
}

func (s *fakeSchemaSource) LoadSchema(context.Context) (*Schema, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return NewSchema("analytics_db", []Table{{Name: s.tableName}}, time.Now()), nil
}

func TestSchemaStoreIsNotReadyUntilLoaded(t *testing.T) {
	source := &fakeSchemaSource{loadErr: errors.New("starrocks down")}
	store := NewSchemaStore(source, time.Minute, discardLogger)

	require.Error(t, store.Refresh(context.Background()))
	assert.ErrorIs(t, store.Ready(context.Background()), ErrSchemaNotLoaded)

	source.loadErr = nil
	source.tableName = "orders"
	require.NoError(t, store.Refresh(context.Background()))
	require.NoError(t, store.Ready(context.Background()))

	schema, err := store.Current()
	require.NoError(t, err)
	assert.Equal(t, "orders", schema.Tables[0].Name)
	assert.Equal(t, 2, source.grants, "every refresh re-grants the read-only user")
}

func TestSchemaStoreKeepsTheLastSchemaOnFailure(t *testing.T) {
	source := &fakeSchemaSource{tableName: "orders"}
	store := NewSchemaStore(source, time.Minute, discardLogger)
	require.NoError(t, store.Refresh(context.Background()))

	source.grantErr = errors.New("grant failed")
	require.Error(t, store.Refresh(context.Background()))

	schema, err := store.Current()
	require.NoError(t, err)
	assert.Equal(t, "orders", schema.Tables[0].Name)
}

func TestSchemaStoreRunStopsWithContext(t *testing.T) {
	store := NewSchemaStore(&fakeSchemaSource{tableName: "orders"}, time.Hour, discardLogger)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		store.Run(ctx)
		close(done)
	}()

	require.Eventually(t, func() bool { return store.Ready(ctx) == nil }, time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
