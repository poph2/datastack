# Shipment Service: Fulfillment Engine

| | |
|---|---|
| Port | `8085` |
| Kubernetes service | `shipment-service` |
| Database | `shipments` |
| Tables | `shipments` |
| Called by | Order Service |
| Calls | none (leaf node) |

## Purpose

The Shipment Service represents putting boxes on trucks. It is the asynchronous tail of checkout: it turns a completed digital transaction into a physical logistics workflow that takes real (simulated) time.

## Responsibilities

- **Logistics tracking.** Assigns a carrier (`FEDEX`, `UPS`, `DHL`) and generates a simulated tracking number.
- **Simulated real-world delay.** Packing a box isn't instantaneous. A background worker waits an artificial, jittered delay before it marks the shipment shipped.
- **Fulfillment state.** Moves each shipment from `PENDING` (label printed) to `SHIPPED` (box on the truck).

## Data Model

Schema as specified:

```sql
CREATE TABLE shipments (
    id SERIAL PRIMARY KEY,
    order_id INT,
    carrier VARCHAR(50), -- FEDEX, UPS, DHL
    tracking_number VARCHAR(100),
    status VARCHAR(20), -- PENDING, SHIPPED
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TRIGGER shipments_set_updated_at
  BEFORE UPDATE ON shipments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

### Proposed additions

```sql
ALTER TABLE shipments ADD COLUMN destination_country VARCHAR(50); -- received in the create payload
ALTER TABLE shipments ADD COLUMN shipped_at TIMESTAMP;             -- explicit SLA end timestamp
CREATE UNIQUE INDEX shipments_order_id_uq ON shipments (order_id); -- one shipment per order, idempotent create
```

`shipped_at` matters. `updated_at` is overwritten by every later update, such as a future `DELIVERED` status, so it can't be the source for "time to ship". Without `shipped_at`, SLA queries would break as soon as a new state is added.

## API

### `POST /shipments/create`

Request:

```json
{ "order_id": 123, "destination_country": "US" }
```

Behavior (fire-and-forget):

1. Choose a carrier. The choice can depend on the destination (for example DHL weighted higher outside the US) so the carrier mix is interesting.
2. Generate a tracking number (for example `1Z` + 16 alphanumerics for UPS, a 12-digit number for FedEx).
3. `INSERT` a row with `status = 'PENDING'`. If a shipment for this `order_id` already exists, return it unchanged (idempotent).
4. Hand the shipment ID to the background packing worker.
5. Respond **immediately**, without waiting for packing.

| Status | Body |
|---|---|
| `202 Accepted` | `{"shipment_id": 777, "order_id": 123, "carrier": "UPS", "tracking_number": "1Z...", "status": "PENDING"}` |
| `400 Bad Request` | `{"error": "INVALID_REQUEST", ...}` |

### `GET /shipments/{order_id}`

Optional read endpoint for debugging and demo drill-down.

## Background Packing Worker

```mermaid
stateDiagram-v2
  [*] --> PENDING: POST /shipments/create
  PENDING --> SHIPPED: worker after packing delay
  SHIPPED --> [*]
```

- A buffered channel feeds a fixed pool of worker goroutines (`PACKING_WORKERS`).
- Each worker sleeps for `PACKING_DELAY_MIN` to `PACKING_DELAY_MAX` with jitter, then runs: `UPDATE shipments SET status='SHIPPED', shipped_at=NOW() WHERE id=$1 AND status='PENDING'`.
- **Load-sensitive latency:** because the pool is fixed, shipments queue up during k6 bursts and time-to-ship grows. This is what makes "is fulfillment slowing down under load?" show up in the dashboards.
- **Crash recovery:** the in-memory queue is lost on restart. At startup, and periodically afterwards, a sweeper re-enqueues rows still `PENDING` that are older than `PACKING_DELAY_MAX`, so no shipment stays stuck forever.
- **Tracing:** the worker starts a new span **linked** to the request span (`trace.WithLinks`) rather than a child, because it outlives the HTTP request. Tempo can still navigate from the checkout trace to the packing work.

## Interactions

```mermaid
sequenceDiagram
  participant O as Order
  participant S as Shipment
  participant W as Packing worker
  O->>S: POST /shipments/create {order_id, destination_country}
  S->>S: INSERT (PENDING)
  S-->>O: 202 Accepted
  Note over O: order -> SHIPPED, checkout returns
  S->>W: enqueue shipment_id
  W->>W: sleep (packing time)
  W->>S: UPDATE status = SHIPPED, shipped_at = NOW()
```

The Order Service calls this as the last step of checkout and doesn't wait for packing. The order is marked `SHIPPED` once this service accepts the request; the real ship time lives in `shipments.shipped_at`. See open question 5 in the [ecosystem README](README.md#7-open-design-questions).

## Configuration

| Variable | Example |
|---|---|
| `PORT` | `8085` |
| `DATABASE_URL` | `postgres://demo:demo@postgresql:5432/shipments?sslmode=disable&timezone=UTC` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |
| `OTEL_SERVICE_NAME` | `shipment-service` |
| `PACKING_WORKERS` | `4` |
| `PACKING_DELAY_MIN` / `PACKING_DELAY_MAX` | `2s` / `30s` |
| `PACKING_QUEUE_SIZE` | `10000` |
| `GOLD_PRIORITY` | `false`: optional. If `true`, accept `loyalty_tier` in the payload and use shorter delays for GOLD, so the VIP SLA chart shows a difference |

## Telemetry

- Inbound spans via `otelhttp.NewHandler`.
- Linked `shipment.pack` spans from the worker, with attributes `shipment.id`, `order.id`, `shipment.carrier`, `shipment.packing_delay_ms`.

## Analytics Impact

This is the main data source for operations and supply-chain dashboards:

- **Fulfillment latency:** `shipped_at - orders.created_at`, as percentiles over time. Does it get slower during load tests?
- **Carrier distribution:** share of volume by `carrier`, and by `destination_country`.
- **VIP SLA compliance:** time-to-ship by `customers.loyalty_tier` (`shipments ⋈ orders ⋈ customers`). Are GOLD customers' boxes packed faster than BRONZE?
- **Backlog:** count of `PENDING` shipments over time.
