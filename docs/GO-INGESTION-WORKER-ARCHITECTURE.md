# Go Ingestion Worker Architecture

This is the detailed spec for DataStack's Go ingestion worker, referenced from [../CLAUDE.md](../CLAUDE.md). Like the orchestrator and microservices, it ships as an internal package of the single Go binary (see [MICROSERVICES-AND-ORCHESTRATOR-ARCHITECTURE.md](MICROSERVICES-AND-ORCHESTRATOR-ARCHITECTURE.md) for that context) — it does not run as its own process.

## 1. Executive Summary And Core Purpose

The ingestion worker is the high-throughput, low-latency bridge between the four PostgreSQL microservice databases (`customer`, `inventory`, `sales`, `invoicing`) and **StarRocks**, the target analytical warehouse. (ClickHouse is not an ingestion target for this subsystem — see "Known Gaps" below.)

It implements a zero-ETL micro-batching pattern based on `updated_at` timestamp watermarking. Independent goroutines poll each source table in parallel, buffer rows in memory, and flush micro-batches into StarRocks via its HTTP Stream Load API. Targets: sub-second ingestion latency and low, steady memory use.

```
┌───────────────────────────────────────────────────────────────────────────┐
│                    GO INGESTION WORKER (internal package)                 │
│                                                                           │
│  1. SCHEMA REFLECTION & COMMENT EXTRACTION LAYER                          │
│     ├── Queries pg_description & information_schema from each source DB  │
│     ├── Generates/syncs corresponding tables in StarRocks                │
│     └── Builds in-memory Tier 1 & Tier 2 semantic context (see below)    │
│                                                                           │
│  2. CONCURRENT WATERMARK POLLING ENGINE                                   │
│     ├── Goroutine 1: polls customer.customers   WHERE updated_at > wm    │
│     ├── Goroutine 2: polls inventory.products   WHERE updated_at > wm    │
│     ├── Goroutine 3: polls sales.orders         WHERE updated_at > wm    │
│     └── Goroutine 4: polls invoicing.invoices   WHERE updated_at > wm    │
│                                                                           │
│  3. HIGH-SPEED BATCH SINK & WATERMARK COMMIT                              │
│     ├── Writes records in micro-batches via StarRocks Stream Load        │
│     └── Advances the in-memory high-watermark once StarRocks acks        │
└───────────────────────────────────────────────────────────────────────────┘
```

## 2. Ingestion Engine Lifecycle And Workflow

The worker runs as a continuous, multi-goroutine lifecycle with three phases.

### Phase 1: Boot, Reflection And Analytical DDL Generation

- **Source schema inspection**: on startup, connects to all four Postgres databases and inspects table structure, data types, primary keys, and developer comments stored in `pg_description` (table and column comments).
- **Target schema synchronization**: inspects StarRocks; if target tables don't exist, creates them, translating Postgres types to StarRocks equivalents (e.g. `TIMESTAMPTZ` → `DATETIME`, `VARCHAR` → `VARCHAR`, `NUMERIC` → `DECIMAL`).
- **Comment preservation**: table/column comments from the source are attached to the generated StarRocks DDL via `COMMENT '...'`.
- **Semantic context packaging**: extracted comments are combined with runtime watermarks/throughput into the Two-Tier Semantic Context payload (section 3) — built here, consumed by the agent package (section 3 also covers the ownership boundary).

### Phase 2: Concurrent Watermark Polling Loop

- One goroutine per source table (4 total for the first version).
- Each goroutine keeps an in-memory `last_watermark` timestamp. On boot, it initializes this from `MAX(updated_at)` in the corresponding StarRocks table (falling back to January 1st of the current year if the table is empty), so a worker restart resumes near where it left off instead of always starting from zero.
- Every polling cycle (default 250ms, configurable), it runs a non-blocking read:

```sql
SELECT * FROM source_table
WHERE updated_at > $1
ORDER BY updated_at ASC
LIMIT 5000;
```

- Fetched rows are parsed directly into memory buffers — no intermediate disk writes, no heavy ORM mapping.

### Phase 3: Micro-Batch Flushing And State Commitment

- Rows accumulate in a per-table memory buffer. A flush triggers when the buffer reaches 1,000 rows **or** 250ms passes without a flush, whichever comes first.
- **Bulk ingestion**: uses the StarRocks HTTP Stream Load API (CSV or JSON payload via HTTP `PUT` directly into StarRocks).
- **Watermark advance**: on a successful HTTP 200 from StarRocks, the worker atomically advances `last_watermark` to the highest `updated_at` value in the flushed batch.
- **Backfill lag is acceptable**: during the Orchestrator's fast-forward historical backfill (1 simulated day per real second), Postgres receives large write bursts. The worker is not required to keep up in real time during that phase — it's fine for it to fall behind and catch up once the backfill ends and the Orchestrator switches to its 250ms real-time tick.

## 3. Two-Tier Semantic Context: Built Here, Used By The Agent Package

The ingestion worker **builds and exposes** the semantic context described below; it does not talk to Ollama itself. Per [CLAUDE.md](../CLAUDE.md), talking to Ollama for reasoning/SQL generation is the Go Agent Package's job. The ingestion worker's job ends at making this context available in-process (e.g. via a shared Go interface/struct) for the agent package to read when it assembles its system prompt.

```
┌───────────────────────────────────────────────────────────────────────────┐
│                    TWO-TIER SEMANTIC CONTEXT (built by ingestion)          │
│                                                                           │
│  TIER 1: STATIC SOURCE METADATA (source developer context)               │
│  ├── Table comment: "Primary transactional record for completed         │
│  │   checkouts"                                                          │
│  ├── Column comment (total_amount): "Final order cost in USD including  │
│  │   tax and shipping"                                                   │
│  └── Source database & microservice domain: "sales (Sales                │
│      microservice)"                                                      │
│                                                                           │
│  TIER 2: DYNAMIC OPERATIONAL METADATA (runtime pipeline context)         │
│  ├── Data freshness / last watermark: "2026-09-23T04:15:00Z (2s ago)"    │
│  ├── Ingestion throughput: "320 records/sec"                             │
│  └── Current table row count: "1,420,500 rows"                          │
└───────────────────────────────────────────────────────────────────────────┘
```

- **Extraction**: the worker queries Postgres system catalogs (`pg_class`, `pg_description`, `pg_attribute`) to read developer comments during Phase 1.
- **Exposure**: table/column comments (Tier 1) and live freshness/throughput/row-count stats (Tier 2) are kept in an in-memory structure the agent package can query.
- **Impact**: when the agent package builds its Ollama prompt, it gets both developer intent (e.g. "divide `amount_cents` by 100 for dollar values") and operational state (e.g. "data is current as of 2 seconds ago"), reducing hallucinated SQL.

## 4. Telemetry And Observability Specification

The ingestion worker registers its metrics on the **same shared `/metrics` endpoint** as the rest of the binary (no dedicated port) — consistent with the rest of the stack, where each Go binary exposes one `/metrics` HTTP endpoint covering all its internal packages.

**Ingestion performance metrics**

- `ingestion_records_synced_total` (counter, by `source_db`, `table_name`) — total rows written to StarRocks.
- `ingestion_batch_duration_seconds` (histogram, by `source_db`, `table_name`) — time to execute a bulk flush.
- `ingestion_bytes_transferred_total` (counter, by `source_db`, `table_name`) — payload size pushed to StarRocks.

**Watermark and pipeline health metrics**

- `ingestion_watermark_timestamp_seconds` (gauge, by `source_db`, `table_name`) — current high-watermark, in epoch seconds.
- `ingestion_lag_seconds` (gauge, by `source_db`, `table_name`) — `time.Now() - last_watermark`, i.e. pipeline freshness.

## 5. Architectural Optimizations And Edge-Case Resilience

- **Postgres index alignment**: every source table should have an index on `(updated_at, id)` so watermark polling stays fast without full table scans.
- **Idempotent upserts**: StarRocks target tables use the `PRIMARY KEY` model with the record's natural ID (`customer_id`, `sku`, `order_id`, `invoice_id`) as the key, so duplicate or re-fetched rows overwrite cleanly instead of duplicating — this is also what makes "resume from StarRocks max(updated_at) on restart" safe even if it slightly overlaps the last flushed batch.
- **Graceful backpressure**: if StarRocks slows down or its Stream Load calls start failing/timing out, the worker pauses polling for the affected table and holds pending rows in a buffered channel rather than crashing or unbounded-growing memory.
- **Low-allocation data buffers**: uses `sync.Pool` byte buffers for CSV/JSON payload building to keep allocations low during high-throughput periods (e.g. right after the fast-forward backfill catches up).

## Known Gaps To Resolve Before Implementation

- Exact mechanism for exposing Tier 1/2 context from the ingestion package to the agent package (in-process interface vs. shared struct with a mutex vs. a small internal event bus) is not yet decided.
- Poll interval (250ms default), batch size (1,000 rows), and flush timeout (250ms) are starting points — expect to tune once real throughput is measured.
