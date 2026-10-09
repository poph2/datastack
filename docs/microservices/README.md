# DataStack Microservice Ecosystem

This is the architecture of DataStack's e-commerce microservice ecosystem. It replaces the earlier single-binary orchestrator design (Customer/Inventory/Sales/Invoicing over in-memory Go channels).

The ecosystem has six Go HTTP microservices and a Go CDC sync daemon. It runs on a local Kubernetes (kind) cluster. k6 generates the traffic, OpenTelemetry emits distributed traces, and the sync daemon streams operational data into StarRocks for real-time Grafana dashboards.

> **Status:** being built one service at a time. Built so far: **Customer Service** ([`services/customer`](../../services/customer)), the reference implementation for the conventions in §3, and **Inventory Service** ([`services/inventory`](../../services/inventory)).

## Service Index

| Service | Port | Database | Tables | Role | Doc |
|---|---|---|---|---|---|
| Order Service | 8080 | `orders` | `orders` | Edge API and checkout orchestrator | [order-service.md](order-service.md) |
| Customer Service | 8081 | `customers` | `customers` | Identity, geography, loyalty tier, account state | [customer-service.md](customer-service.md) |
| Marketing Service | 8082 | `marketing` | `campaigns`, `attributions` | Top-of-funnel touchpoints and conversion attribution | [marketing-service.md](marketing-service.md) |
| Inventory Service | 8083 | `inventory` | `inventory` | Stock levels, reservations, product catalog | [inventory-service.md](inventory-service.md) |
| Payment Service | 8084 | `payments` | `payments` | Simulated card processing with injected failures | [payment-service.md](payment-service.md) |
| Shipment Service | 8085 | `shipments` | `shipments` | Simulated fulfillment with artificial packing delay | [shipment-service.md](shipment-service.md) |
| CDC Sync App | n/a | reads all six | n/a | Watermark polling into StarRocks Stream Load | [cdc-sync.md](cdc-sync.md) |

## 1. System Topology

The ecosystem has three layers:

1. **Operational layer (Go + PostgreSQL).** Six independent HTTP REST microservices. Each one owns its own logical database inside a shared PostgreSQL instance. They talk to each other synchronously over HTTP JSON APIs. No service reads another service's database.
2. **Observability layer (OpenTelemetry + Tempo).** Every Go service is instrumented with OpenTelemetry. Spans go to an OTel Collector, are stored in Grafana Tempo, and are viewed in Grafana.
3. **Analytical layer (Go CDC + StarRocks).** A Go sync daemon polls the `updated_at` watermark of every operational table and pushes micro-batches into StarRocks over HTTP Stream Load. Grafana dashboards query StarRocks.

```mermaid
flowchart LR
  k6[k6 load generator]

  subgraph Operational["Operational layer"]
    Order[Order Service :8080]
    Customer[Customer Service :8081]
    Marketing[Marketing Service :8082]
    Inventory[Inventory Service :8083]
    Payment[Payment Service :8084]
    Shipment[Shipment Service :8085]
    PG[(PostgreSQL<br/>6 logical DBs)]
  end

  subgraph Observability["Observability layer"]
    Collector[OTel Collector :4317]
    Tempo[(Grafana Tempo)]
  end

  subgraph Analytics["Analytical layer"]
    Sync[CDC Sync App]
    SR[(StarRocks<br/>analytics_db)]
  end

  Grafana[Grafana]

  k6 -->|POST /customers| Customer
  k6 -->|POST /attributions| Marketing
  k6 -->|POST /orders/checkout| Order

  Order -->|GET /customers/id| Customer
  Order -->|POST /inventory/reserve| Inventory
  Order -->|POST /payments/process| Payment
  Order -->|PUT /attributions/id/order| Marketing
  Order -->|POST /shipments/create| Shipment

  Order & Customer & Marketing & Inventory & Payment & Shipment --> PG
  Order & Customer & Marketing & Inventory & Payment & Shipment -. OTLP gRPC .-> Collector
  Collector --> Tempo

  PG -->|poll updated_at| Sync
  Sync -->|Stream Load| SR

  Grafana --> Tempo
  Grafana --> SR
```

### Call graph

The Order Service is the only service that calls other services. The other five are leaf nodes: they serve requests and write to their own database.

| Caller | Callee | Call | Critical path? |
|---|---|---|---|
| k6 | Customer | `POST /customers` | n/a |
| k6 | Marketing | `POST /attributions` | n/a |
| k6 | Order | `POST /orders/checkout` | n/a |
| Order | Customer | `GET /customers/{id}` | Yes: an invalid or suspended user rejects the checkout |
| Order | Inventory | `POST /inventory/reserve` | Yes: out of stock fails the checkout |
| Order | Payment | `POST /payments/process` | Yes: a decline fails the checkout and releases stock |
| Order | Inventory | `POST /inventory/release` | Compensation only |
| Order | Marketing | `PUT /attributions/{id}/order` | No: best-effort |
| Order | Shipment | `POST /shipments/create` | No: fire-and-forget (accepted immediately) |

## 2. End-to-End User Journey

One k6 iteration simulates one customer journey, from the ad click to the box leaving the warehouse.

```mermaid
sequenceDiagram
  autonumber
  participant k6
  participant M as Marketing
  participant C as Customer
  participant O as Order
  participant I as Inventory
  participant P as Payment
  participant S as Shipment

  k6->>C: POST /customers (or reuse an existing user)
  C-->>k6: {id}
  k6->>M: POST /attributions {user_id, utm_source}
  M-->>k6: {attribution_id}
  k6->>O: POST /orders/checkout {user_id, product_id, attribution_id}
  O->>O: INSERT order (PENDING)
  O->>C: GET /customers/{id}
  C-->>O: {country, loyalty_tier, status}
  O->>I: POST /inventory/reserve
  I-->>O: 200 {unit_price} or 409 OUT_OF_STOCK
  O->>P: POST /payments/process
  P-->>O: SUCCESS or DECLINED
  alt declined
    O->>I: POST /inventory/release
    O->>O: status = FAILED
  else success
    O->>O: status = PAID
    O->>M: PUT /attributions/{id}/order
    O->>S: POST /shipments/create
    S-->>O: 202 Accepted (PENDING)
    O->>O: status = SHIPPED
    S->>S: background: sleep, then SHIPPED
  end
  O-->>k6: checkout result
```

See [order-service.md](order-service.md) for the exact orchestration and failure semantics.

## 3. Shared Conventions

These conventions were established while building the Customer Service ([`services/customer`](../../services/customer)), which is the reference implementation. Every later service follows them.

### 3.1 Universal columns and triggers

Every operational table starts with the same four columns. In Go, they come from embedding `database.Model` ([`pkgs/database/model.go`](../../pkgs/database/model.go)):

| Column | Definition | Purpose |
|---|---|---|
| `id` | `BIGSERIAL PRIMARY KEY` | Auto-incrementing key; tie-breaker in the CDC cursor |
| `created_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | Row creation time; used for latency and cohort analytics |
| `updated_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | CDC watermark; **refreshed on every UPDATE by a trigger** |
| `deleted_at` | `TIMESTAMPTZ` (nullable) | GORM soft delete. Hard deletes are invisible to watermark CDC, so services never `DELETE` rows |

Rules:

- **Postgres is the only clock.** `created_at` and `updated_at` are never written by the application: the model tags them `<-:false;default:now()`, and GORM reads them back with `RETURNING`. If GORM set them from the pod's clock on insert while the trigger used the database's clock on update, clock skew could move `updated_at` backwards, and the sync daemon would skip rows. (An integration test caught exactly this.)
- **Soft delete moves the watermark.** GORM's soft delete is an `UPDATE ... SET deleted_at`, so the trigger moves `updated_at` and the sync daemon sees the delete.
- **Unique constraints are partial**, for example `UNIQUE (email) WHERE deleted_at IS NULL`, so a soft-deleted row doesn't block re-creating it.
- **Every table has a `(updated_at, id)` index** for the sync daemon's cursor query.
- **Enums are `VARCHAR` with a `CHECK` constraint**, mirrored as typed string constants in Go.
- `TIMESTAMPTZ` everywhere, so the session time zone can't affect watermarks.

The service docs that haven't been built yet still show the schemas from the original spec (`SERIAL`, `TIMESTAMP`, `is_deleted`). Each one moves to these conventions when its service is built.

Each service's first migration installs the trigger function, and every table gets a `BEFORE UPDATE` trigger:

```sql
-- 00001_set_updated_at_function.sql
CREATE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- in each table's migration:
CREATE TRIGGER customers_set_updated_at
    BEFORE UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

### 3.2 Schema ownership and migrations

- Each service owns one logical database. In kind, its Helm release declares that database as a CloudNativePG `Database` resource.
- Tables are created only by the service's own versioned **goose** SQL migrations (`services/<name>/migrations/NNNNN_name.sql`), which are embedded in the binary. GORM `AutoMigrate` is not used.
- `<binary> migrate up|down|status` runs them. In Kubernetes, `migrate up` runs as an **initContainer** before `serve` starts. A Postgres advisory lock makes concurrent replicas safe.
- Every migration has a working `-- +goose Down`. The integration tests run up → down → up.

### 3.3 HTTP API conventions

- REST over JSON with **gin**. Requests are validated with gin binding tags (go-playground/validator), and invalid input gets `400 INVALID_REQUEST`.
- Error body: `{"error": "<MACHINE_CODE>", "message": "<human text>"}` (`httpserver.RespondError`). Machine codes such as `OUT_OF_STOCK` and `PAYMENT_DECLINED` are what the Order Service records as `failure_reason`. 500s never leak internals.
- Database models are never serialised directly. Handlers map them to response structs.
- **Status codes:** `400` only for malformed or invalid input, `404` for an unknown entity, and `409` when a valid request conflicts with current state (`EMAIL_EXISTS`, `OUT_OF_STOCK`). Callers branch on the `error` code, never on the status alone.
- **Money** is `shopspring/decimal` in Go and `DECIMAL(10,2)` in Postgres, never `float64`. In JSON it is a fixed two-decimal **string** (`"49.99"`, `"50.00"`, via `StringFixed(2)`), so JavaScript clients can't silently round it. Requests should accept the same form, since `decimal.Decimal` unmarshals both strings and numbers.
- Every service exposes `GET /healthz` (liveness), `GET /readyz` (readiness; pings the DB) and `GET /metrics`. These sit outside the tracing and logging middleware, so probes don't create noise.
- Service-to-service URLs come from environment variables populated by ConfigMaps (for example `PAYMENT_URL=http://payment-service:8084`).
- Outbound HTTP clients use explicit timeouts. No call waits forever.

### 3.4 Configuration

- **cobra** commands: `serve`, `migrate up|down|status`.
- **viper** binds every flag to an environment variable of the same name: `--database-url` ↔ `DATABASE_URL`, `--tier-weights` ↔ `TIER_WEIGHTS`. Flag defaults are the documented defaults.
- In Kubernetes, plain settings go in the chart's ConfigMap. Database credentials come from CNPG's secret as `PGUSER`/`PGPASSWORD`, which pgx reads natively, so passwords never appear in URLs.

### 3.5 Go layout

One Go module at the repo root (`github.com/poph2/datastack`) and one shared `Dockerfile` (`--build-arg SERVICE=<name>`, distroless, non-root).

```
pkgs/                       # shared by every service
  cli/         root cobra command, flag <-> env binding
  database/    GORM open (pool, tracing plugin), base Model
  migrate/     goose runner + `migrate` command
  httpserver/  gin router (otelgin, slog-gin, Prometheus, recovery, probes), error helpers, graceful shutdown
  logging/     slog JSON/text setup
  telemetry/   OTel tracer provider + W3C propagator
  pgtest/      testcontainers Postgres for integration tests
services/<name>/
  main.go      root command: serve + migrate
  serve.go     wiring: logger, tracing, DB, handler, router, run
  migrations/  embedded goose SQL
  internal/<domain>/
    model.go       GORM model + enum types
    repository.go  GORM queries; maps driver errors to domain errors (ErrNotFound, ...)
    handler.go     gin handlers, request/response types, Store interface
    *_test.go      handler tests (fake Store) + repository tests (real Postgres)
deploy/
  charts/go-service/   one generic chart for every service
  values/<name>.yaml   per-service values
  infra/               CNPG Postgres cluster
  kind/                cluster config
```

A service adds a service layer between handler and repository only when it has real business logic. The Order Service's orchestration will have one; the Customer Service doesn't need one.

More patterns, established by the Inventory Service:

- **Concurrency through SQL, not locks.** State changes that must be race-free are one conditional `UPDATE ... WHERE <guard> RETURNING` (GORM: `Model(&x).Clauses(clause.Returning{}).Where(...).Update(col, gorm.Expr(...))`). Under READ COMMITTED, Postgres re-checks the guard against the latest row version, so no explicit transaction or `SELECT ... FOR UPDATE` is needed. A test races many goroutines against it.
- **Background workers** (`restocker.go`) run beside the HTTP server under `errgroup` in `serve.go`, so they share one lifetime and the DB closes only after both stop. Each run is its own root span. A worker must be safe with several replicas running it (the guard-in-`WHERE` trick again), not depend on leader election.
- **Seed data is a goose migration** (`00003_seed_catalog.sql`), deterministic, so the existing `migrate up` initContainer loads it and every environment has the same IDs.
- **Domain metrics** live in the domain package's `metrics.go` (`<service>_<thing>_total`, low-cardinality labels such as `outcome`). The shared HTTP metrics count requests; domain metrics count what happened to the business.

### 3.6 Testing

- Handler tests go through the real shared router (`httpserver.NewRouter`) with a fake `Store`.
- Repository tests run the real migrations against Postgres in testcontainers (`pkgs/pgtest`) and assert the schema behaviour: triggers, constraints, soft delete.
- `make test` runs everything (needs Docker). `make test-unit` (`go test -short`) skips the containers.

## 4. Telemetry (OpenTelemetry)

The goal is one trace per checkout, spanning every service it touches. This depends on the W3C `traceparent` header being propagated.

| Concern | Implementation |
|---|---|
| SDK | `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/sdk/trace` |
| Propagation | `otel.SetTextMapPropagator(propagation.TraceContext{})`. **This is required:** without it, nothing injects `traceparent` |
| Inbound spans | `otelgin.Middleware("<service>")` on the API route group (`pkgs/httpserver`) |
| Outbound spans | HTTP clients use `otelhttp.NewTransport(http.DefaultTransport)`, and requests must be built with the inbound `ctx` (`http.NewRequestWithContext`) |
| DB spans | GORM OpenTelemetry plugin (`gorm.io/plugin/opentelemetry`), with query variables omitted |
| Exporter | OTLP gRPC to `otel-collector:4317`, configured by the standard `OTEL_*` env vars. If `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, spans are created (trace IDs still appear in logs) but not exported |
| Resource | `service.name=<name>-service`, `deployment.environment=kind` |

Request logs (slog JSON via `slog-gin`) include `trace_id` and `span_id`, so a log line links straight to its trace. Each service also exposes Prometheus metrics at `/metrics`: `http_server_requests_total{route,method,status}` and `http_server_request_duration_seconds`, plus Go runtime metrics.

The collector forwards to Tempo, and Grafana uses Tempo as a datasource. In Tempo's service graph the Order Service appears as the hub, with edges to all five other services.

## 5. Analytics Pipeline

The CDC Sync App runs one polling loop per table, every 5 seconds. Each loop reads rows whose `updated_at` is newer than its watermark (oldest first, batches of up to 1000) and pushes the batch into StarRocks `analytics_db` with HTTP Stream Load. When StarRocks confirms the load, the loop advances the watermark in `watermarks.json`. Full details are in [cdc-sync.md](cdc-sync.md).

Tables synced (seven tables across six databases):

| Source DB | Table | StarRocks target |
|---|---|---|
| `customers` | `customers` | `analytics_db.customers` |
| `marketing` | `campaigns` | `analytics_db.campaigns` |
| `marketing` | `attributions` | `analytics_db.attributions` |
| `inventory` | `inventory` | `analytics_db.inventory` |
| `payments` | `payments` | `analytics_db.payments` |
| `shipments` | `shipments` | `analytics_db.shipments` |
| `orders` | `orders` | `analytics_db.orders` |

### Dashboards this enables

| Dashboard | Joins |
|---|---|
| Executive heartbeat: orders/min, revenue/hour, checkout success rate | `orders` |
| Revenue by country and loyalty tier, CLTV, cohorts | `orders` ⋈ `customers` |
| Campaign ROI, ROAS, CAC | `campaigns` ⋈ `attributions` ⋈ `payments` |
| Revenue leakage: declined payments by segment | `payments` ⋈ `orders` ⋈ `customers` |
| Stock depletion velocity and lost revenue | `inventory` ⋈ `orders` |
| Fulfillment SLA: time-to-ship, carrier mix, VIP SLA | `shipments` ⋈ `orders` ⋈ `customers` |

## 6. Deployment (kind Kubernetes)

| Component | Delivery |
|---|---|
| Six microservices, CDC Sync App, k6 runner | Images built locally and loaded with `kind load docker-image` |
| Service wiring | The generic `deploy/charts/go-service` chart, installed once per service as release `<name>-service`: Deployment (migrate initContainer, probes, preStop drain, non-root/read-only), ClusterIP Service, ConfigMap (peer URLs such as `PAYMENT_URL`, plus `OTEL_EXPORTER_OTLP_ENDPOINT`), and a CNPG `Database` |
| PostgreSQL | CloudNativePG operator (`cnpg/cloudnative-pg`) running one `Cluster` (`deploy/infra/postgres.yaml`). Each service's chart release declares its own logical database. Bitnami's chart was dropped because its free images were discontinued in 2025 |
| Traces | `open-telemetry/opentelemetry-collector`, `grafana/tempo` |
| Dashboards | `grafana/grafana`, with Tempo and StarRocks (MySQL protocol, FE port 9030) datasources provisioned |
| Analytics | `starrocks/starrocks` (kube-starrocks), overridden to 1 FE + 1 BE with 2 GB memory limits |
| CDC Sync App | One Deployment with a single replica (the watermark file must have a single writer), plus a small PVC for `watermarks.json` |
| Load | k6 as a continuous Deployment (steady demo traffic) or a CronJob (bursts) |

Make targets (every `kubectl`/`helm` call pins `--context kind-datastack`):

```sh
make kind-up            # create the kind cluster
make infra              # CNPG operator + Postgres cluster
make deploy-customer    # build image, kind load, helm upgrade --install customer-service
make forward-customer   # port-forward localhost:8081
make up && make run-customer   # or: local Postgres via docker compose + go run
```

Internal DNS names: `order-service`, `customer-service`, `marketing-service`, `inventory-service`, `payment-service`, `shipment-service`, `otel-collector`, `starrocks-fe`.

### Load generator (k6)

The k6 script loops through these weighted actions:

1. Register a new customer (`POST /customers`), or pick a known user ID from earlier iterations.
2. Log an ad click (`POST /attributions`) with a random `utm_source` from the seeded campaigns.
3. Check out (`POST /orders/checkout`) with a random `product_id` from the seeded catalog, carrying the `attribution_id` forward. A configurable fraction of checkouts send no attribution, to simulate organic traffic.

## 7. Open Design Questions

These are gaps between the original spec and the per-service narratives. Each service doc proposes a resolution, and they are collected here so they can be decided once:

1. **Account state column.** The Customer Service must reject suspended users, but `customers` has no status column. Proposed: add `status`. See [customer-service.md](customer-service.md).
2. **Order line data.** "Who bought what" and the lost-revenue dashboard need `product_id` and `quantity` on `orders`, and the failure dashboards need `failure_reason`. Proposed: add all three. See [order-service.md](order-service.md).
3. **Pricing source.** The checkout payload carries no amount. Proposed: `POST /inventory/reserve` returns `unit_price`, and the Order Service computes `total_amount`.
4. **Inventory release.** The checkout flow releases stock on payment failure, but no endpoint was specified. Proposed: `POST /inventory/release`.
5. **SHIPPED semantics.** The Order Service marks the order `SHIPPED` as soon as the Shipment Service accepts the request, while the shipment itself is still `PENDING`. This keeps orders simple, but it means "shipped" in `orders` means "handed to fulfillment"; use `shipments` for the actual ship time. An alternative is a `FULFILLING` order status.
6. **Campaign join key.** `attributions` stores only `utm_source`, while `campaigns` is keyed by `utm_source` + `utm_campaign`. ROAS per campaign needs `utm_campaign` on `attributions` as well.
7. **Shipment destination.** `POST /shipments/create` receives `destination_country`, but `shipments` doesn't store it. Proposed: add the column.
8. **Runtime target.** `CLAUDE.md` still describes Docker Compose, NATS, ClickHouse, and Prometheus. This design uses kind, synchronous HTTP, StarRocks, and OTel/Tempo. `CLAUDE.md` should be updated to match once this design is accepted.
