# Contributing to DataStack

Thanks for taking an interest. DataStack is a demo system, so the bar for a change is slightly
unusual: it should make the thing easier to understand or more honest about how it behaves, not just
add surface area.

## Before you start

Two documents are worth reading, and they will answer most review comments before they happen:

- **[docs/microservices/README.md §3](docs/microservices/README.md)** — the shared conventions every
  service follows.
- **[.github/skills/go-clean-code/SKILL.md](.github/skills/go-clean-code/SKILL.md)** — the Go style
  this repository uses.

## Running it

The full stack needs Docker with about 12 GB and 6 CPUs; see the
[demo runbook](docs/demo-runbook.md). For most changes you don't need the cluster at all:

```sh
make up              # just Postgres, in Docker Compose
make run-customer    # run one service against it
make test            # everything, including testcontainers-backed repository tests
make test-unit       # the same, minus anything that needs Docker
make lint            # gofmt, go vet, helm lint
```

## The conventions that matter most

- **The GORM model is the schema.** `AutoMigrate`, named CHECK constraints, partial unique indexes
  on `deleted_at IS NULL`, soft deletes only, and Postgres as the only clock for `created_at` and
  `updated_at`.
- **Every column gets a `comment:` tag and every model a `TableComment()`**, written for someone —
  or something — that has never seen the system: meaning, units, enum values, what NULL means, and
  `[ref: table.column]` on every reference. This is not decoration; it is the analyzer's semantic
  layer. After changing a model run `make catalog` and commit the regenerated
  [data catalog](docs/microservices/data-catalog.md).
- **Money is `shopspring/decimal`**, serialised as a fixed two-decimal string. Never `float64`.
- **Concurrency is guarded SQL**, not locks or read-then-write: `UPDATE ... WHERE <guard>
  RETURNING`. Race-sensitive SQL needs a concurrency test.
- **HTTP status codes mean something**: `400` bad input, `404` unknown entity, `409` conflict with
  current state. A business failure is data (4xx), not a 5xx.
- **Fault injection is a `pkgs/chaos` knob** owned by the domain type, read live on the hot path.
  Never a flag that needs a restart.
- **Every workload declares CPU and memory requests and limits.** If you change one, update the
  budget table in [.github/skills/local-resources/SKILL.md](.github/skills/local-resources/SKILL.md).

## Tests

- Handler tests go through the real `httpserver.NewRouter` with a fake store.
- Repository tests run the service's real `Migrate` against Postgres via `pkgs/pgtest`, which needs
  Docker. `make test-unit` skips them.
- Dashboard changes: validate the SQL against StarRocks and the PromQL against Prometheus before
  committing, then run `make dashboards`.

## Pull requests

Run `make lint` and `make test` first. Keep the change focused, explain what you observed rather
than only what you changed, and say what you verified — especially for anything that only shows up
under load or in the cluster.
