# Customer Service: Identity and Segmentation

| | |
|---|---|
| Port | `8081` |
| Kubernetes service | `customer-service` |
| Database | `customers` |
| Tables | `customers` |
| Called by | k6, Order Service |
| Calls | none (leaf node) |

## Purpose

The Customer Service is the source of truth for **who** is interacting with the business. It deliberately knows nothing about transactions, products, or campaigns. It only knows the person behind the screen: where they are, what loyalty tier they're in, and whether their account is in good standing.

## Responsibilities

- **Profile management.** Stores baseline identity (email) and geography (country) for every simulated user.
- **Loyalty and segmentation.** Holds the tier (`BRONZE`, `SILVER`, `GOLD`) for each user. Downstream analytics use it to test business rules, for example whether Gold members generate more revenue or whether Bronze members fail checkouts more often.
- **Account state.** Tracks whether a user is active, suspended, or pending deletion. The Order Service uses this to accept or reject checkouts.

## Data Model

Schema as specified:

```sql
CREATE TABLE customers (
    id SERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE,
    country VARCHAR(50),
    loyalty_tier VARCHAR(20), -- BRONZE, SILVER, GOLD
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TRIGGER customers_set_updated_at
  BEFORE UPDATE ON customers
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

### Proposed addition: account state

The account-state responsibility needs a column the base schema doesn't have:

```sql
ALTER TABLE customers
  ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE'; -- ACTIVE, SUSPENDED, PENDING_DELETION
```

- `is_deleted` remains the CDC soft-delete flag (the row is logically gone).
- `status = 'PENDING_DELETION'` is a business state: the user asked to delete their account, but the row still exists and is visible to analytics.

## API

### `POST /customers`

Registers a new user. k6 calls this to simulate sign-up.

Request:

```json
{ "email": "ada@example.com", "country": "US", "loyalty_tier": "BRONZE" }
```

- `email` is required and must be unique. k6 should generate unique emails (for example with the VU ID plus the iteration number).
- `country` is required (ISO-3166 alpha-2 recommended).
- `loyalty_tier` is optional. If it's omitted, the service assigns one with a weighted random draw (for example 70% `BRONZE`, 22% `SILVER`, 8% `GOLD`) so segments have realistic proportions.

Responses:

| Status | Body |
|---|---|
| `201 Created` | `{"id": 1234, "email": "...", "country": "US", "loyalty_tier": "BRONZE", "status": "ACTIVE", "created_at": "..."}` |
| `400 Bad Request` | `{"error": "INVALID_REQUEST", ...}` |
| `409 Conflict` | `{"error": "EMAIL_EXISTS", ...}` |

### `GET /customers/{id}`

Returns one user. k6 calls it to "fetch" an existing user before starting a session. The Order Service calls it to validate the buyer during checkout.

| Status | Body |
|---|---|
| `200 OK` | `{"id": 1234, "email": "...", "country": "US", "loyalty_tier": "GOLD", "status": "ACTIVE"}` |
| `404 Not Found` | `{"error": "CUSTOMER_NOT_FOUND", ...}`, also returned when `is_deleted = TRUE` |

The service reports `status` as-is and doesn't decide whether a checkout is allowed. The Order Service treats anything other than `ACTIVE` as a rejection.

### Optional: lifecycle simulation

To make loyalty and account-state analytics move over time, the service can expose admin endpoints for k6 (or run a background job):

- `PATCH /customers/{id}`: change `loyalty_tier` or `status` (upgrades, suspensions).
- `DELETE /customers/{id}`: sets `is_deleted = TRUE` (soft delete only).

## Interactions

```mermaid
flowchart LR
  k6 -->|POST /customers| CS[Customer Service]
  k6 -->|GET /customers/id| CS
  OS[Order Service] -->|GET /customers/id| CS
  CS --> DB[(customers DB)]
```

- **Upstream, k6:** registration and session-start lookups.
- **Upstream, Order Service:** a synchronous validation call in the checkout critical path. The Order Service uses the result to accept or reject the checkout, and it reads `country` to use as the shipment destination.

Because this call sits in the checkout critical path, `GET /customers/{id}` must be a single primary-key lookup with no joins.

## Configuration

| Variable | Example |
|---|---|
| `PORT` | `8081` |
| `DATABASE_URL` | `postgres://demo:demo@postgresql:5432/customers?sslmode=disable&timezone=UTC` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |
| `OTEL_SERVICE_NAME` | `customer-service` |
| `TIER_WEIGHTS` | `BRONZE=70,SILVER=22,GOLD=8` |

## Telemetry

- Inbound spans via `otelhttp.NewHandler`. During checkout, `GET /customers/{id}` appears as a child of the Order Service span.
- Useful span attributes: `customer.id`, `customer.loyalty_tier`, `customer.status`.

## Analytics Impact

Once synced to StarRocks, `customers` is the main **dimension table**. It answers "who" and "where" for every other dataset:

- Revenue by country and by loyalty tier (`orders ⋈ customers`).
- Customer lifetime value (CLTV) and cohort retention, by `created_at` month.
- Payment declines by segment (`payments ⋈ orders ⋈ customers`).
- VIP SLA: GOLD vs BRONZE time-to-ship (`shipments ⋈ orders ⋈ customers`).
