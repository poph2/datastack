# Microservices And Orchestrator Architecture

This is the detailed spec for DataStack's Go orchestrated data engine (the Orchestrator plus the Customer/Inventory/Sales/Invoicing microservices), referenced from [../CLAUDE.md](../CLAUDE.md). It supersedes any earlier NATS/separate-simulator-container description — this subsystem runs as **one Go binary**. Other subsystems (ingestion, backend API, agent, UI) will get their own architecture documents.

## 1. Core Philosophy And Key Constraints

A single-binary, high-concurrency system in Go that simulates a realistic e-commerce business across four independent domain microservices: **Customer, Inventory, Sales, and Invoicing**. An Orchestrator Engine controls a simulated business clock and drives dynamic transactions across all four domains.

Key constraints:

- **Decentralized, self-managed migrations**: the Orchestrator never executes database migrations or touches schema logic. Each microservice manages its own database lifecycle, running GORM `AutoMigrate` against its dedicated database independently during its own boot phase.
- **Zero HTTP/network overhead**: inter-service messaging and command dispatch happen exclusively through strongly-typed, in-memory Go channels.
- **Fast-forward timeline backfill (1 real second = 1 simulated day)**: on startup, the Orchestrator fast-forwards through historical time — from January 1st of the current year up to today — at a rate of 1 simulated day per real-time second. In roughly 4-5 minutes this generates most of a year's worth of historical data before seamlessly transitioning into a real-time transaction stream.
- **Deterministic referential integrity**: the Orchestrator maintains an internal, thread-safe in-memory cache of valid entity IDs (Customer IDs and Inventory SKUs), populated by the domain services, so downstream services (Sales) never fail due to missing master data.
- **Granular observability**: every microservice and the Orchestrator emit real-time Prometheus metrics over a background HTTP endpoint, exposing throughput, payload volume, execution latency, state cache size, and the moving simulated epoch timestamp.

## 2. Microservice Domain And Lifecycle Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        GO APPLICATION RUNTIME                           │
│                                                                         │
│  [ PROMETHEUS METRIC ENGINE ]                                          │
│  └── Background HTTP endpoint exposing gauges, counters, histograms    │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ INDEPENDENT BOOT & DECENTRALIZED AUTO-MIGRATION PHASE              │ │
│  │                                                                    │ │
│  │  [Customer Service]  [Inventory Service]  [Sales Service]  [Invoicing Service] │
│  │  ├─ Connect customer     ├─ Connect inventory   ├─ Connect sales     ├─ Connect invoicing  │
│  │  └─ GORM AutoMigrate      └─ GORM AutoMigrate       └─ GORM AutoMigrate  └─ GORM AutoMigrate     │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  [ ORCHESTRATOR TICK ENGINE ]                                           │
│  └── Boots after all microservices finish migrating.                   │
│      Controls the Jan 1 -> today simulated timeline.                   │
│                                                                         │
│      │ CreateCustomerCmd      │ RestockInventoryCmd    │ CreateSaleCmd  │
│      ▼                        ▼                        ▼               │
│  [Customer Service]      [Inventory Service]      [Sales Service]      │
│  ├─ command channel      ├─ command channel        ├─ command channel  │
│  ├─ writes customer       ├─ writes inventory     ├─ writes sales      │
│  └─ updates state cache  └─ updates state cache     └─ emits OrderCreatedEvent │
│                                                                │        │
│                                                                ▼        │
│                                                      [Invoicing Service] │
│                                                      ├─ event channel    │
│                                                      └─ writes invoicing │
└─────────────────────────────────────────────────────────────────────────┘
```

Not pictured above for space: the Orchestrator also sends `ProcessPaymentCmd` directly to the Invoicing microservice (independent of the Sales → `OrderCreatedEvent` path) to drive payment settlement/failure scenarios.

Each domain connects to its own dedicated Postgres database and runs its own migration logic at boot:

**Customer microservice (`customer`)**

- Startup: connects to `customer`, runs GORM `AutoMigrate` for the `customers` table before opening its command channel.
- Role: master data repository for user accounts.
- Input: `CreateCustomerCmd` from the Orchestrator.
- Behavior: generates a unique customer ID, writes the record stamped with the simulated command timestamp, updates the shared state cache with the new Customer ID.
- Output: none (terminal write service).

**Inventory microservice (`inventory`)**

- Startup: connects to `inventory`, runs GORM `AutoMigrate` for the `products` table.
- Role: manages product catalog master data and stock levels.
- Input: `RestockInventoryCmd` from the Orchestrator.
- Behavior: upserts product SKUs or updates stock quantities with the simulated timestamp, updates the state cache with valid SKUs.
- Output: none (terminal write service).

**Sales microservice (`sales`)**

- Startup: connects to `sales`, runs GORM `AutoMigrate` for the `orders` table.
- Role: processes e-commerce checkout transactions.
- Input: `CreateSaleCmd` from the Orchestrator.
- Behavior: validates the command references a valid Customer ID and SKU, calculates order totals, writes the order stamped with the simulated date, emits an async `OrderCreatedEvent`.
- Output: `OrderCreatedEvent` to the Invoicing microservice, over a Go channel.

**Invoicing microservice (`invoicing`)**

- Startup: connects to `invoicing`, runs GORM `AutoMigrate` for the `invoices` table.
- Role: handles financial ledger entries and billing.
- Input: `OrderCreatedEvent` from the Sales microservice, and `ProcessPaymentCmd` sent directly by the Orchestrator (payment settlement/failure events, independent of order creation).
- Behavior: processes both asynchronously, generates or updates an invoice record with a payment status stamped with the simulated date.
- Output: none (terminal write service).

## 3. Data Storage And Schema Design

Each microservice owns a dedicated, logically isolated Postgres database. Tables are auto-migrated strictly by the owning service at boot. Every schema should include `COMMENT ON TABLE`/`COMMENT ON COLUMN` so downstream static schema extraction can give the agent good semantic context.

**`customer` → `customers`**

- `customer_id` (string, PK) — formatted `CUST-XXXXXX`.
- `full_name` (string).
- `email` (string, indexed) — unique.
- `created_at` (timestamptz, indexed) — stamped with the simulated date.
- `updated_at` (timestamptz, indexed) — auto-updated on modification.

**`inventory` → `products`**

- `sku` (string, PK) — formatted `SKU-XXXX`.
- `product_name` (string).
- `stock_quantity` (integer).
- `unit_price` (numeric).
- `created_at` (timestamptz, indexed) — stamped with the simulated date.
- `updated_at` (timestamptz, indexed) — updated on restock/modification.

**`sales` → `orders`**

- `order_id` (string, PK) — formatted `ORD-XXXXXX`.
- `customer_id` (string, indexed) — references the Customer microservice.
- `total_amount` (numeric).
- `status` (string) — defaults to `COMPLETED`.
- `created_at` (timestamptz, indexed) — stamped with the simulated date.
- `updated_at` (timestamptz, indexed) — updated on state change.

**`invoicing` → `invoices`**

- `invoice_id` (UUID, PK).
- `order_id` (string, indexed) — references the Sales order.
- `payment_status` (string) — defaults to `PAID`.
- `amount_paid` (numeric).
- `created_at` (timestamptz, indexed) — stamped with the simulated date.
- `updated_at` (timestamptz, indexed) — updated on state change.

## 4. Orchestrator Engine And Fast-Forward Backfill Algorithm

The Orchestrator contains zero database migration or connection code — it is purely a time engine and scenario generator.

```
                ┌────────────────────────────────┐
                │        ORCHESTRATOR ENGINE       │
                └────────────────┬─────────────────┘
                                 │
        ┌────────────────────────┼────────────────────────┐
        ▼                        ▼                        ▼
1. Service Wait Phase   2. Jan 1 Calibration    3. Fast-Forward Backfill Loop
 Wait for all 4          Set sim_time = Jan 1     Ticker: 1 real second = 1 sim day
 microservices to        Seed base entities        Generate daily commands
 finish AutoMigrate      dated Jan 1 into cache     Advance sim_time by +1 day
 and signal ready                                              │
                                                                 ▼
                                                  4. Live Real-Time Transition
                                                   Triggers when sim_time >= now()
                                                   Switches ticker to a 250ms live stream
```

1. **Service wait phase** — wait until all four domain microservices finish connection pooling, run their own GORM `AutoMigrate`, and start listening on their command channels.
2. **January 1st calibration phase** — set the simulated clock to Jan 1, 00:00:00 UTC of the current year; send an initial batch of `CreateCustomerCmd`/`RestockInventoryCmd` stamped Jan 1 so master data exists from the start; wait until the Customer/Inventory state caches are populated.
3. **Fast-forward backfill loop (1 real second = 1 simulated day)** — a ticker fires every 1000ms, each tick representing one full simulated day:
   - Generate 1-5 new customer accounts for that day.
   - Replenish stock / introduce new SKUs as needed.
   - Generate a batch of 10-30 orders for that day, picking valid Customer IDs/SKUs from the state cache.
   - Advance `sim_time` by one day.
   - Update the `app_orchestrator_simulated_time_timestamp_seconds` gauge with the simulated day's epoch timestamp.
   - At this rate, backfilling ~265 days (Jan 1 to late September) takes roughly 4-5 minutes.
4. **Real-time live transition** — once `sim_time >= time.Now().UTC()`, log "Historical backfill complete. Transitioning to real-time ticker." and switch the tick interval from 1 second/day down to 250ms/live-tick using real wall-clock timestamps.

## 5. Message Payloads And In-Memory Channel Flow

All inter-service communication is non-blocking and memory-resident, over typed Go channels:

```
[Orchestrator] --CreateCustomerCmd--> [Customer Service]
[Orchestrator] --RestockInventoryCmd--> [Inventory Service]
[Orchestrator] --CreateSaleCmd--> [Sales Service] --OrderCreatedEvent--> [Invoicing Service]
[Orchestrator] --ProcessPaymentCmd--> [Invoicing Service]
```

- **`CreateCustomerCmd`** — `FullName` (string), `Email` (string), `Created` (simulated time). Producer: Orchestrator. Consumer: Customer.
- **`RestockInventoryCmd`** — `SKU` (string), `ProductName` (string), `Quantity` (int), `UnitPrice` (float), `Created` (simulated time). Producer: Orchestrator. Consumer: Inventory.
- **`CreateSaleCmd`** — `CustomerID` (string), `SKU` (string), `Quantity` (int), `Created` (simulated time). Producer: Orchestrator. Consumer: Sales.
- **`OrderCreatedEvent`** — `OrderID` (string), `CustomerID` (string), `SKU` (string), `Amount` (float), `Quantity` (int), `Created` (simulated time). Producer: Sales. Consumer: Invoicing.
- **`ProcessPaymentCmd`** — `OrderID` (string), `PaymentStatus` (string: `PAID` or `FAILED`), `Amount` (float), `Created` (simulated time). Producer: Orchestrator. Consumer: Invoicing. Drives the "a payment fails" demo scenario independently of order creation.

## 6. Observability And Telemetry Specification

Each service in this binary runs a background HTTP server (e.g. `:2112/metrics`) using the Prometheus client library.

**Orchestrator metrics**

- `app_orchestrator_ticks_total` (counter) — incremented on every tick.
- `app_orchestrator_simulated_time_timestamp_seconds` (gauge) — the moving simulated epoch timestamp.
- `app_orchestrator_commands_dispatched_total` (counter, by `target_service`, `command_type`).

**Microservice performance and activity metrics**

- `app_service_events_processed_total` (counter, by `service`, `status`).
- `app_service_processing_duration_seconds` (histogram, by `service`, `action`).
- `app_service_bytes_written_total` (counter, by `service`).

**State cache metrics**

- `app_orchestrator_active_state_cache_count` (gauge, by `entity_type`: `customer`, `inventory`).

## 7. Operational Optimizations

- **Decoupled boot sequence** — microservices initialize independently and own their own connections/migrations; the Orchestrator stays lightweight and schema-agnostic.
- **Database connection pooling** — each microservice configures its own pool (25 max open connections) to handle fast-forward backfill throughput without exhaustion.
- **Thread-safe memory caching** — `RWMutex`-protected state cache lets the Orchestrator do high-speed reads during backfill without blocking microservices from appending new entity IDs.
- **Buffered channel queues** — 500-message channel buffers absorb temporary write spikes during the 1-second-per-day fast-forward backfill without blocking the Orchestrator clock.

## Known Gaps To Resolve Before Implementation

- The current `docker-compose.yml`/`postgres/init` now use the `customer`/`inventory`/`sales`/`invoicing` database names described above; the `nats` service has been removed from Compose since this subsystem uses in-memory channels instead.
- Exact Prometheus metrics port for this binary (`:2112` above) vs. the convention already used elsewhere in the stack (each Go service exposing `/metrics` on its own service port) should be reconciled.
