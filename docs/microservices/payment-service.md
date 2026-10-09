# Payment Service: Cash Register

| | |
|---|---|
| Port | `8084` |
| Kubernetes service | `payment-service` |
| Database | `payments` |
| Tables | `payments` |
| Called by | Order Service |
| Calls | none (leaf node) |

## Purpose

The Payment Service stands in for a third-party gateway such as Stripe or PayPal. It processes charges and, just as importantly, **fails some of them on purpose**. If every payment succeeded, the demo analytics would have nothing interesting to show.

## Responsibilities

- **Transaction processing.** Takes an order amount and a payment method, decides an outcome, and records the transaction.
- **Simulated failures.** Randomly declines a configurable share of charges (5% by default) to simulate declined cards, insufficient funds, or fraud triggers.
- **Payment state.** Records the money's state: `SUCCESS` or `DECLINED`, plus `REFUNDED` later if returns are simulated.

## Data Model

Schema as specified:

```sql
CREATE TABLE payments (
    id SERIAL PRIMARY KEY,
    order_id INT,
    amount DECIMAL(10,2),
    method VARCHAR(50), -- CREDIT_CARD, PAYPAL
    status VARCHAR(20), -- SUCCESS, DECLINED
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TRIGGER payments_set_updated_at
  BEFORE UPDATE ON payments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

### Proposed addition

```sql
ALTER TABLE payments ADD COLUMN decline_reason VARCHAR(50); -- CARD_DECLINED, INSUFFICIENT_FUNDS, FRAUD_SUSPECTED
```

This lets the revenue-leakage dashboard break declines down by cause.

Every attempt is recorded, including declines. A declined row is a real fact that the analytics depend on.

## API

### `POST /payments/process`

Request:

```json
{ "order_id": 123, "amount": 99.99, "method": "CREDIT_CARD" }
```

- `order_id` and `amount` are required.
- `method` is optional. If it's omitted, the service picks one with a weighted draw (for example 75% `CREDIT_CARD`, 25% `PAYPAL`).

Processing:

1. Validate the input (`amount > 0`).
2. Optionally sleep for a short jitter (for example 20 to 150 ms) to mimic gateway latency, so the span is visible in traces.
3. Draw a random number. If it is below `FAILURE_RATE`, the outcome is `DECLINED` with a random `decline_reason`. Otherwise it is `SUCCESS`.
4. `INSERT` the payment row and return its outcome.

| Status | Body |
|---|---|
| `200 OK` | `{"payment_id": 5001, "order_id": 123, "status": "SUCCESS"}` |
| `402 Payment Required` | `{"error": "PAYMENT_DECLINED", "payment_id": 5002, "decline_reason": "INSUFFICIENT_FUNDS"}` |
| `400 Bad Request` | `{"error": "INVALID_REQUEST", ...}` |

The Order Service treats any non-`200` response, a timeout, or a transport error as a failed payment. It then releases the inventory and fails the order.

### Optional: `POST /payments/{id}/refund`

A future endpoint for simulating returns. It sets `status = 'REFUNDED'`, which bumps `updated_at` so the CDC sync picks it up.

## Failure Injection

| Variable | Default | Effect |
|---|---|---|
| `FAILURE_RATE` | `0.05` | Share of charges declined |
| `FAILURE_RATE_BY_METHOD` | (unset) | Optional per-method override, for example `PAYPAL=0.02,CREDIT_CARD=0.07` |
| `LATENCY_MIN_MS` / `LATENCY_MAX_MS` | `20` / `150` | Simulated gateway latency |

Seed the random number generator per process. For demos that need to be repeatable, also accept a `RANDOM_SEED`.

## Interactions

```mermaid
sequenceDiagram
  participant O as Order
  participant P as Payment
  participant I as Inventory
  O->>P: POST /payments/process {order_id, amount}
  alt SUCCESS
    P-->>O: 200 {status: SUCCESS}
    Note over O: order -> PAID
  else DECLINED
    P-->>O: 402 PAYMENT_DECLINED
    O->>I: POST /inventory/release
    Note over O: order -> FAILED (PAYMENT_DECLINED)
  end
```

The Payment Service is the final financial gatekeeper. It never calls another service, and it isn't responsible for compensation; that is the Order Service's job.

## Configuration

| Variable | Example |
|---|---|
| `PORT` | `8084` |
| `DATABASE_URL` | `postgres://demo:demo@postgresql:5432/payments?sslmode=disable&timezone=UTC` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |
| `OTEL_SERVICE_NAME` | `payment-service` |
| `FAILURE_RATE` | `0.05` |

## Telemetry

- Inbound spans via `otelhttp.NewHandler`.
- Span attributes: `order.id`, `payment.amount`, `payment.method`, `payment.status`, `payment.decline_reason`.
- Set span status to error on declines, so failed checkouts stand out in Tempo.

## Analytics Impact

`payments` is the bedrock of finance and revenue-assurance dashboards:

- **Processed vs attempted revenue:** `SUM(amount) WHERE status='SUCCESS'` vs `SUM(amount)`.
- **Revenue leakage:** declined dollars per day, by decline reason, by payment method, and by customer segment (`payments ⋈ orders ⋈ customers`).
- **ROAS denominator:** cleared payments joined through `attributions` give the true revenue per campaign.
