// Package pgtest starts a throwaway Postgres container for integration tests.
package pgtest

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// image matches the major version the kind cluster runs.
const image = "postgres:18-alpine"

// Start runs a fresh Postgres for the calling test, removes it when the test
// finishes, and returns its connection URL. Tests that call it are skipped
// under `go test -short`, so unit tests can run without Docker.
func Start(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode")
	}

	ctx := context.Background()
	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("starting postgres container: %v", err)
	}

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("reading postgres connection string: %v", err)
	}
	return url
}
