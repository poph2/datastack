// Package pgtest starts a throwaway Postgres container for integration tests.
package pgtest

import (
	"context"
	"os"
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

	// Each service's test package runs as its own process, in parallel, and
	// every process tried to start testcontainers' shared reaper container.
	// Those starts race and fail intermittently. CleanupContainer below already
	// removes each container when its test ends, so the reaper is only needed
	// if the test binary is killed mid-run (then: docker rm -f the leftovers).
	// testcontainers reads this once, on first use in the process.
	if err := os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true"); err != nil {
		t.Fatalf("disabling testcontainers reaper: %v", err)
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
