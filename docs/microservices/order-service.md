# Order Service: Checkout Orchestrator

> **Status: built.** Code is in [`services/order`](../../services/order). It's the only service that calls others, and it introduced the shared service-to-service client ([`pkgs/httpclient`](../../pkgs/httpclient)).

| | |
|---|---|
| Port | `8080` |
| Kubernetes service | `order-service` |
| Binary | `order` (`serve`, `migrate`) |
| Database | `orders` |
| Tables | `orders` |
| Called by | k6 (the edge API) |
| Calls | Customer, Inventory, Payment, Marketing, Shipment |

## Purpose

The Order Service is the central nervous system of the ecosystem. Every other service cares only about its own domain. The Order Service conducts the checkout so that the pieces come together into one coherent transaction: no charging for stock that doesn't exist, and no shipping when the payment was declined. In the Tempo service graph it's the hub, with edges to every other service.

## Responsibilities

- **Transaction state.** Records every checkout attempt and tracks its lifecycle: `PENDING` → `PAID` → `SHIPPED`, or `FAILED` at any step.
- **Distributed orchestration.** Runs the checkout as an ordered sequence of synchronous calls, releasing reserved stock when payment fails.
- **Financial record.** Who bought what, when, and for exactly how much (GMV). It also records *why* a failed order failed.
- **Recovery.** A background reconciler finishes orders that were paid but whose shipment request didn't go through, and resolves orders stranded in `PENDING` by a crash mid-checkout.

## Data Model

The schema is the GORM model in [`internal/order/model.go`](../../services/order/internal/order/model.go), applied by `order migrate`:

```go
type Order struct {
	database.Model // id, created_at/updated_at (DB clock), deleted_at, (updated_at, id) index
	UserID        uint64  `gorm:"not null"`
	AttributionID *uint64 // nil for organic traffic
	ProductID     string  `gorm:"size:50;not null"`
	Quantity      int     `gorm:"not null;check:chk_orders_quantity,quantity >= 1"`
	UnitPrice     *decimal.Decimal `gorm:"type:decimal(10,2)"` // set once inventory reserves
	TotalAmount   *decimal.Decimal `gorm:"type:decimal(10,2)"`
	Status        Status          `gorm:"size:20;not null;index;check:chk_orders_status,..."`
	FailureReason *FailureReason  `gorm:"size:50;check:chk_orders_failure_reason,..."`
}
```

Changes from the original spec: `product_id`, `quantity`, `unit_price` and `failure_reason` were added (they were the proposed additions), and the shared conventions apply. `user_id` and `attribution_id` are logical references to other services' databases, not foreign keys.

**Failure reasons** are the peers' own error codes: `CUSTOMER_INVALID`, `CUSTOMER_SUSPENDED`, `OUT_OF_STOCK`, `PRODUCT_NOT_FOUND`, `PAYMENT_DECLINED`, `UPSTREAM_ERROR`.

### Status lifecycle

```mermaid
stateDiagram-v2
  [*] --> PENDING: order row inserted
  PENDING --> FAILED: customer invalid / suspended, out of stock, payment declined, upstream error (or the reconciler, for a stranded unpaid order)
  PENDING --> PAID: payment SUCCESS (checkout, or the reconciler for a stranded paid order)
  PAID --> SHIPPED: shipment accepted (checkout, or the reconciler later)
  FAILED --> [*]
  SHIPPED --> [*]
```

Every transition is one **guarded UPDATE** (`Repository.Apply`: `WHERE id = ? AND status = <from> RETURNING *`), so the checkout and the reconciler (or two replicas) can never both move the same order. A test races 20 goroutines on one transition and exactly one wins.

## API

### `POST /orders/checkout`

```json
{ "user_id": 1, "product_id": "PROD_0001", "attribution_id": 99, "quantity": 1 }
```

`user_id` and `product_id` are required. `attribution_id` is optional (organic traffic has none). `quantity` is optional, defaults to 1, max 100.

| Status | Body |
|---|---|
| `201 Created` | The order, `status` `SHIPPED`, with `tracking_number`. Also `201` with `status` `PAID` if the shipment request failed; the reconciler finishes it |
| `422 Unprocessable Entity` | Business failure: `{"error": "OUT_OF_STOCK", "message": "...", "order": {...FAILED order...}}`. Same for `CUSTOMER_INVALID`, `CUSTOMER_SUSPENDED`, `PRODUCT_NOT_FOUND`, `PAYMENT_DECLINED` |
| `502 Bad Gateway` | `UPSTREAM_ERROR`: a critical dependency was unreachable, timed out, or answered unexpectedly. Same body shape |
| `400 Bad Request` | `INVALID_REQUEST`. No order is created |
| `500` | The Order Service's own database failed |

Business failures are deliberately not 5xx, so k6's server-error rate means "the system is broken", not "a card was declined". The failure body carries the FAILED order, so callers still learn its ID.

Order response fields: `order_id`, `user_id`, `attribution_id`, `product_id`, `quantity`, `unit_price`, `total_amount` (money as fixed two-decimal strings, `null` until priced), `status`, `failure_reason`, `tracking_number` (checkout only), `created_at`, `updated_at`.

### `GET /orders/{id}`

`200` with the order, or `404 ORDER_NOT_FOUND`.

## Orchestration Flow

[`checkout.go`](../../services/order/internal/order/checkout.go):

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
  alt 404 / not ACTIVE / unreachable
    O->>O: FAILED (CUSTOMER_INVALID / CUSTOMER_SUSPENDED / UPSTREAM_ERROR)
    O-->>k6: 422 / 502
  end
  O->>I: POST /inventory/reserve
  alt 409 / 404
    O->>O: FAILED (OUT_OF_STOCK / PRODUCT_NOT_FOUND)
    O-->>k6: 422
  end
  O->>O: set unit_price, total_amount
  O->>P: POST /payments/process {order_id, amount}
  alt 402 / error
    O->>I: POST /inventory/release (compensate)
    O->>O: FAILED (PAYMENT_DECLINED / UPSTREAM_ERROR)
    O-->>k6: 422 / 502
  end
  O->>O: PAID
  opt attribution_id present (best effort)
    O->>M: PUT /attributions/{id}/order {order_id, order_amount}
  end
  O->>S: POST /shipments/create {order_id, destination_country, loyalty_tier}
  S-->>O: 202 (PENDING)
  O->>O: SHIPPED
  O-->>k6: 201
```

### Failure semantics

| Failure point | Order status | `failure_reason` | Compensation |
|---|---|---|---|
| Customer 404 | `FAILED` | `CUSTOMER_INVALID` | none |
| Customer not `ACTIVE` | `FAILED` | `CUSTOMER_SUSPENDED` | none |
| Inventory 409 / 404 | `FAILED` | `OUT_OF_STOCK` / `PRODUCT_NOT_FOUND` | none |
| Payment 402 | `FAILED` | `PAYMENT_DECLINED` | release stock |
| Payment `409 ALREADY_PAID` | continues as paid | n/a | none. The order already has a successful charge, and Payment's guard is what prevents paying twice |
| Customer/Inventory/Payment unreachable | `FAILED` | `UPSTREAM_ERROR` | release stock if it was reserved |
| Marketing fails | unaffected | n/a | none. The click stays unconverted (logged at WARN) |
| Shipment fails | stays `PAID` | n/a | the reconciler retries it |

**A started checkout always finishes.** It runs on `context.WithoutCancel` of the request context (keeping the trace), so a caller that disconnects mid-checkout (a k6 pod being replaced, a client timeout) can't strand the order in PENDING with stock reserved. Each peer call keeps its own timeout, so a checkout still can't hang. Load testing on kind found this: restarting k6 cancelled in-flight checkouts halfway.

If releasing stock fails, it's logged at ERROR, and the Inventory restock loop eventually covers the leak. The service isn't a durable saga engine: if the process dies mid-checkout, an order is stranded in `PENDING`, possibly with stock reserved. The reconciler resolves such orders (below).

### Reconciler

[`reconciler.go`](../../services/order/internal/order/reconciler.go) runs beside the HTTP server under the `errgroup`, like Inventory's restocker. Every `RECONCILE_INTERVAL` it runs two passes under one `order.reconcile` root span. Ages are measured with the database clock (`updated_at < NOW() - interval`), and each pass handles up to 50 orders.

**1. Stranded `PENDING` orders.** It loads orders that have sat untouched in `PENDING` for longer than `PENDING_AFTER` (default `2m`, far above the longest possible checkout: 2s + 2s + 2s + 500ms + 1s of peer timeouts). For each one:

1. **Claim it** with a guarded no-op transition, `UPDATE orders SET status = 'PENDING' WHERE id = ? AND status = 'PENDING' AND updated_at = <the value it read>`. The trigger moves `updated_at`, so a second replica holding the same read (or a checkout that touched the order since) matches nothing and leaves it alone. That is what makes the release below happen at most once.
2. **Ask the Payment Service** `GET /payments?order_id=<id>&status=SUCCESS`.
3. **Charged:** `PENDING → PAID` (guarded). The next runs' `PAID` pass ships it once it has been `PAID` for `RECONCILE_AFTER`. Its amount is added to `orders_revenue_total`.
4. **Not charged:** if the order has a `unit_price`, stock was reserved (the checkout prices the order right after a successful reservation), so it calls `POST /inventory/release`. Then `PENDING → FAILED` with `failure_reason = UPSTREAM_ERROR` (guarded).

If the payment lookup or the release fails, the order stays `PENDING` (its `updated_at` moved with the claim) and is retried once it is older than `PENDING_AFTER` again. An unknown payment state is never treated as "not paid".

Known gap: a checkout that crashes after reserving but before recording `unit_price` leaves an unpriced order, so its stock is not released (the restock loop covers it), and one that crashes between releasing stock after a decline and failing the order gets its stock released twice. Both windows are a single statement wide.

**2. Stuck `PAID` orders.** It loads orders that have been `PAID` for longer than `RECONCILE_AFTER`, re-reads each customer, and retries the shipment. Retrying is safe because the Shipment Service creates at most one shipment per order, and `PAID → SHIPPED` is guarded, which also makes it safe with several replicas. It never charges again.

Each resolved `PENDING` order is an `order.pending_resolved` event on the run's span (with `order.id` and `order.reconcile_outcome`).

## Service-to-Service Calls

[`clients.go`](../../services/order/internal/order/clients.go) wraps one [`httpclient.Client`](../../pkgs/httpclient/client.go) per peer:

- `otelhttp` transport, and every request built from the inbound context, so the whole checkout is **one trace**.
- A timeout per peer. Critical calls (customer, inventory, payment) default to 2s, marketing to 500ms, shipment to 1s.
- Error responses become `*httpclient.APIError` carrying the peer's error code. The typed clients turn the codes they branch on into sentinel errors (`ErrOutOfStock`, `ErrPaymentDeclined`, ...). Anything else is `UPSTREAM_ERROR`.
- Money is sent as fixed two-decimal strings.
- **No automatic retries.** Reserve, release and charge aren't idempotent per request.
- `clients_test.go` replays response bodies copied from each peer's real handler, pinning the contract.

## Chaos

Runtime failure injection for live demos, from the shared [`pkgs/chaos`](../../pkgs/chaos) package:

- `GET /admin/chaos` returns `{"service": "<name>", "knobs": [{"name", "description", "value", "default", "min", "max"}, ...]}`, `latency_ms` first.
- `PUT /admin/chaos` with a partial object such as `{"latency_ms": 500}` changes those knobs and returns the same body. It is all-or-nothing: an unknown knob or a value outside `[min, max]` (or a fraction for a whole-number knob) is `400 INVALID_REQUEST`, and nothing changes.
- `DELETE /admin/chaos` resets every knob to its default.

The `/admin` routes are traced, logged and metered like the API, but never delayed by `latency_ms`. Every change is logged at INFO with the old and new value, and the gauge `chaos_knob_value{knob}` shows the current values. **Values live in the pod's memory**: a restart resets every knob to its default, and with several replicas each pod has its own values (every service runs one replica in kind).

| Knob | Range | Default | Effect |
|---|---|---|---|
| `latency_ms` | 0 to 5000, whole | `0` | Delay before every API request is handled (not `/admin`, probes or `/metrics`). It ends early if the caller gives up (`503 REQUEST_CANCELLED`), and shows on the request span as the attribute `chaos.latency_ms` plus `chaos.latency.start`/`chaos.latency.end` events |

`latency_ms` here slows every checkout as k6 sees it. On a peer, a `latency_ms` above `CRITICAL_TIMEOUT` (2s) turns that peer's calls into timeouts, so checkouts fail with `UPSTREAM_ERROR`.

## Configuration

| Flag / env var | Default |
|---|---|
| `--http-addr` / `HTTP_ADDR` | `:8080` |
| `--database-url` / `DATABASE_URL` | none |
| `--customer-url` / `CUSTOMER_URL` | `http://localhost:8081` (kind: `http://customer-service:8081`) |
| `--marketing-url` / `MARKETING_URL` | `http://localhost:8082` |
| `--inventory-url` / `INVENTORY_URL` | `http://localhost:8083` |
| `--payment-url` / `PAYMENT_URL` | `http://localhost:8084` |
| `--shipment-url` / `SHIPMENT_URL` | `http://localhost:8085` |
| `--critical-timeout` / `CRITICAL_TIMEOUT` | `2s` |
| `--marketing-timeout` / `MARKETING_TIMEOUT` | `500ms` |
| `--shipment-timeout` / `SHIPMENT_TIMEOUT` | `1s` |
| `--reconcile-interval` / `RECONCILE_INTERVAL` | `30s` (`0` disables) |
| `--reconcile-after` / `RECONCILE_AFTER` | `30s` |
| `--pending-after` / `PENDING_AFTER` | `2m` (must exceed the longest checkout) |

## Running It

```sh
make deploy-all          # every service, leaf services first
make forward-order       # localhost:8080
curl -XPOST localhost:8080/orders/checkout -H 'content-type: application/json' \
  -d '{"user_id":1,"product_id":"PROD_0001"}'
make load RATE=10        # continuous k6 journeys inside the cluster
```

## Telemetry

- The checkout server span from `otelgin`, plus one client span per downstream call (`POST inventory-service`, ...). On `FAILED` the server span's status is set to error with the reason, so failed checkouts can be filtered in Tempo.
- Span attributes: `order.id`, `order.status`, `order.total_amount`, `order.failure_reason`, `customer.id`, `product.id`, `attribution.id`.
- `order.reconcile` root spans from the reconciler, with `order.stuck_pending` and `order.stuck` counts and one `order.pending_resolved` event per resolved PENDING order.

| Metric | Labels | |
|---|---|---|
| `orders_checkouts_total` | `status`, `reason` | Final outcome of each checkout |
| `orders_revenue_total` | | Dollars of successfully paid orders |
| `orders_reconciled_total` | `outcome` | Stuck orders the reconciler handled: `shipped` (PAID retried), `paid` (stranded PENDING had been charged), `released` (stranded PENDING failed, stock released), `cancelled` (stranded PENDING failed, nothing reserved), `failed` (attempt failed, retried later) |
| `http_client_requests_total` | `peer`, `method`, `status` | From `pkgs/httpclient`. `status="error"` means a transport failure or timeout |
| `http_client_request_duration_seconds` | `peer`, `method` | Latency of each downstream call as the Order Service sees it |

## Analytics Impact

This table drives the executive dashboards:

- **Heartbeat:** orders per minute, revenue per hour (`SUM(total_amount) WHERE status IN ('PAID','SHIPPED')`).
- **Checkout success rate:** shipped divided by all orders. Every attempt is a row, so the denominator is honest.
- **Failure breakdown:** count and dollar value by `failure_reason`, showing exactly where the business is losing money.
- **Join hub:** `orders` links customers (`user_id`), marketing (`attribution_id`), inventory (`product_id`), payments and shipments (`order_id`).
