# Order Service: Checkout Orchestrator

| | |
|---|---|
| Port | `8080` |
| Kubernetes service | `order-service` |
| Database | `orders` |
| Tables | `orders` |
| Called by | k6 (the edge API) |
| Calls | Customer, Inventory, Payment, Marketing, Shipment |

## Purpose

The Order Service is the central nervous system of the ecosystem. Every other service cares only about its own domain. The Order Service conducts the checkout so that the pieces come together into one coherent transaction: no charging for stock that doesn't exist, and no shipping when the payment was declined.

In the Tempo service graph it is the large hub node, with edges to every other service.

## Responsibilities

- **Transaction state.** Creates the order and tracks its lifecycle: `PENDING` → `PAID` → `SHIPPED`, or `FAILED` at any step.
- **Distributed orchestration.** Runs the checkout as an ordered sequence of synchronous calls, with compensation when a step fails.
- **Financial record.** Holds the top-line facts: who bought what, when, and for exactly how much (GMV). It also records *why* a failed order failed.

## Data Model

Schema as specified:

```sql
CREATE TABLE orders (
    id SERIAL PRIMARY KEY,
    user_id INT,
    attribution_id INT,
    total_amount DECIMAL(10,2),
    status VARCHAR(20), -- PENDING, PAID, FAILED, SHIPPED
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TRIGGER orders_set_updated_at
  BEFORE UPDATE ON orders
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

### Proposed additions

The narrative ("who bought **what**", "records exactly **why** an order failed", lost-revenue joins with inventory) needs columns the base schema lacks:

```sql
ALTER TABLE orders ADD COLUMN product_id VARCHAR(50);
ALTER TABLE orders ADD COLUMN quantity INT NOT NULL DEFAULT 1;
ALTER TABLE orders ADD COLUMN unit_price DECIMAL(10,2);
ALTER TABLE orders ADD COLUMN failure_reason VARCHAR(50); -- CUSTOMER_INVALID, CUSTOMER_SUSPENDED, OUT_OF_STOCK, PRODUCT_NOT_FOUND, PAYMENT_DECLINED, UPSTREAM_ERROR
```

`user_id` and `attribution_id` are logical references into other services' databases. They are not foreign keys.

### Status lifecycle

```mermaid
stateDiagram-v2
  [*] --> PENDING: order row inserted
  PENDING --> FAILED: customer invalid / out of stock / payment declined / upstream error
  PENDING --> PAID: payment SUCCESS
  PAID --> SHIPPED: shipment accepted
  FAILED --> [*]
  SHIPPED --> [*]
```

Every transition is an `UPDATE`, so it bumps `updated_at`. That means the CDC sync sees every state change, not just the final one. When traffic is fast, though, intermediate states may be overwritten between polls; see [cdc-sync.md](cdc-sync.md).

## API

### `POST /orders/checkout`

Request:

```json
{ "user_id": 1, "product_id": "PROD_A", "attribution_id": 99, "quantity": 1 }
```

- `user_id` and `product_id` are required.
- `attribution_id` is optional. Organic traffic has none.
- `quantity` is optional and defaults to `1`.

Responses:

| Status | Body |
|---|---|
| `201 Created` | `{"order_id": 999, "status": "SHIPPED", "total_amount": 49.99, "tracking_number": "1Z..."}` |
| `422 Unprocessable Entity` | `{"order_id": 999, "status": "FAILED", "error": "OUT_OF_STOCK"}`. Also used for `CUSTOMER_INVALID`, `CUSTOMER_SUSPENDED`, `PRODUCT_NOT_FOUND`, `PAYMENT_DECLINED` |
| `502 Bad Gateway` | `{"order_id": 999, "status": "FAILED", "error": "UPSTREAM_ERROR"}`: a critical dependency was unreachable or timed out |
| `400 Bad Request` | `{"error": "INVALID_REQUEST"}`: malformed payload; no order row is created |

Business failures are not server errors. Returning `422` keeps k6's HTTP error rate meaningful: only `5xx` means "the system is broken".

### `GET /orders/{id}`

Returns the order row. Useful for debugging and demo drill-down.

## Orchestration Flow

```mermaid
sequenceDiagram
  autonumber
  participant k6
  participant O as Order
  participant C as Customer
  participant I as Inventory
  participant P as Payment
  participant M as Marketing
  participant S as Shipment

  k6->>O: POST /orders/checkout
  O->>O: INSERT order (PENDING)
  O->>C: GET /customers/{user_id}
  alt 404 or status != ACTIVE
    O->>O: FAILED (CUSTOMER_INVALID / CUSTOMER_SUSPENDED)
    O-->>k6: 422
  end
  O->>I: POST /inventory/reserve {product_id, quantity}
  alt 409 / 404
    O->>O: FAILED (OUT_OF_STOCK / PRODUCT_NOT_FOUND)
    O-->>k6: 422
  end
  O->>O: set unit_price, total_amount
  O->>P: POST /payments/process {order_id, amount}
  alt declined or error
    O->>I: POST /inventory/release (compensate)
    O->>O: FAILED (PAYMENT_DECLINED)
    O-->>k6: 422
  end
  O->>O: PAID
  opt attribution_id present (best-effort)
    O->>M: PUT /attributions/{id}/order {order_id, order_amount}
  end
  O->>S: POST /shipments/create {order_id, destination_country}
  S-->>O: 202 (PENDING)
  O->>O: SHIPPED
  O-->>k6: 201
```

Step by step:

1. **Create the order.** `INSERT` with `status = 'PENDING'`, `user_id`, `attribution_id`, `product_id`, `quantity`. The `order_id` must exist before payment, and inserting first means *every* checkout attempt is recorded, including ones rejected at step 2. This is what makes the checkout success rate and failure reason breakdown accurate.
2. **Validate the customer.** `GET /customers/{user_id}`. A `404` gives `FAILED`/`CUSTOMER_INVALID`. A `status` other than `ACTIVE` gives `FAILED`/`CUSTOMER_SUSPENDED`. Keep `country` for step 6.
3. **Reserve inventory.** `POST /inventory/reserve`. `OUT_OF_STOCK` or `PRODUCT_NOT_FOUND` gives `FAILED`. The money is never touched. On success, store `unit_price` and `total_amount = unit_price × quantity`.
4. **Charge payment.** `POST /payments/process` with `order_id` and `total_amount`. Any non-`200` response, timeout, or transport error is a failure: call `POST /inventory/release` to compensate, then mark `FAILED`/`PAYMENT_DECLINED` (or `UPSTREAM_ERROR` for a transport error). On success, mark `PAID`.
5. **Report the conversion (best-effort).** If `attribution_id` is set, call `PUT /attributions/{id}/order`. Log failures and record them on the span, but **don't** fail the order: marketing isn't in the critical path.
6. **Trigger fulfillment.** `POST /shipments/create` with `destination_country` from step 2. The Shipment Service answers `202` straight away.
7. **Mark the order shipped.** `UPDATE status = 'SHIPPED'` and return `201` to k6.

### Failure semantics

| Failure point | Order status | `failure_reason` | Compensation |
|---|---|---|---|
| Customer 404 | `FAILED` | `CUSTOMER_INVALID` | none |
| Customer not ACTIVE | `FAILED` | `CUSTOMER_SUSPENDED` | none |
| Inventory 409 | `FAILED` | `OUT_OF_STOCK` | none |
| Inventory 404 | `FAILED` | `PRODUCT_NOT_FOUND` | none |
| Payment 402 | `FAILED` | `PAYMENT_DECLINED` | release inventory |
| Customer/Inventory/Payment unreachable | `FAILED` | `UPSTREAM_ERROR` | release inventory if it was reserved |
| Marketing fails | unaffected | n/a | none; attribution stays unconverted |
| Shipment fails | stays `PAID` | n/a | none; a reconciler can retry `PAID` orders older than N seconds |

The service isn't a durable saga engine. If the process dies mid-checkout, the order can be left `PENDING` with stock reserved. That is acceptable for the demo. An optional sweeper can mark stale `PENDING` orders `FAILED`/`UPSTREAM_ERROR` and release their stock.

## HTTP Client Configuration

- One shared `http.Client` with `Transport: otelhttp.NewTransport(http.DefaultTransport)`, so every outbound call carries `traceparent` and produces a client span.
- Always use `http.NewRequestWithContext(ctx, ...)` with the **inbound request context**. Otherwise the trace breaks into separate traces.
- Per-dependency timeouts. Critical calls (customer, inventory, payment) default to 2s. Marketing defaults to 500ms. Shipment defaults to 1s.
- No automatic retries on `POST /payments/process`. A retry could double-charge, because the payment endpoint isn't idempotent per order.

## Configuration

| Variable | Example |
|---|---|
| `PORT` | `8080` |
| `DATABASE_URL` | `postgres://demo:demo@postgresql:5432/orders?sslmode=disable&timezone=UTC` |
| `CUSTOMER_URL` | `http://customer-service:8081` |
| `MARKETING_URL` | `http://marketing-service:8082` |
| `INVENTORY_URL` | `http://inventory-service:8083` |
| `PAYMENT_URL` | `http://payment-service:8084` |
| `SHIPMENT_URL` | `http://shipment-service:8085` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |
| `OTEL_SERVICE_NAME` | `order-service` |
| `CRITICAL_CALL_TIMEOUT` | `2s` |

## Telemetry

- Server span `POST /orders/checkout` (from `otelhttp.NewHandler`), with five downstream client spans (plus a release span on decline).
- Span attributes: `order.id`, `order.status`, `order.failure_reason`, `order.total_amount`, `customer.id`, `product.id`, `attribution.id`.
- Set span status to error on `FAILED`, so failed checkouts can be filtered in Tempo by `status=error`.

## Analytics Impact

This table drives the executive dashboards:

- **Heartbeat:** total orders, orders/minute, revenue/hour (`SUM(total_amount) WHERE status IN ('PAID','SHIPPED')`).
- **Checkout success rate:** shipped divided by all orders.
- **Failure breakdown:** count and dollar value by `failure_reason`, showing exactly where the business is losing money (stock vs declines vs customer issues). It is also a natural source for operational alerts.
- **Join hub:** `orders` links customers (`user_id`), marketing (`attribution_id`), inventory (`product_id`), payments, and shipments (`order_id`).
