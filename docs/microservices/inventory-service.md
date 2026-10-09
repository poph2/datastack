# Inventory Service: Stock Keeper

| | |
|---|---|
| Port | `8083` |
| Kubernetes service | `inventory-service` |
| Database | `inventory` |
| Tables | `inventory` |
| Called by | Order Service (k6 optionally, for restocks) |
| Calls | none (leaf node) |

## Purpose

The Inventory Service checks the storefront against physical reality. It is the source of truth for product availability and catalog metadata, and it makes sure the business never sells stock it doesn't have.

## Responsibilities

- **Stock management.** Maintains the available quantity for every SKU.
- **Concurrency-safe reservations.** If two checkouts race for the last unit, exactly one wins. The reservation is a single atomic conditional update, so it is correct under any number of concurrent requests.
- **Product metadata.** Stores each product's category and unit price. The Order Service gets its price from here.
- **Restock simulation.** Replenishes stock periodically so a long k6 run doesn't drain everything to zero.

## Data Model

Schema as specified:

```sql
CREATE TABLE inventory (
    id SERIAL PRIMARY KEY,
    product_id VARCHAR(50) UNIQUE,
    category VARCHAR(50),
    stock_level INT,
    price DECIMAL(10,2),
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TRIGGER inventory_set_updated_at
  BEFORE UPDATE ON inventory
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

Recommended constraint, so a bug can never push stock negative:

```sql
ALTER TABLE inventory ADD CONSTRAINT stock_non_negative CHECK (stock_level >= 0);
```

### Seed data

At startup the service upserts a fixed catalog (for example 50 to 200 SKUs, `PROD_0001`…) across categories such as `ELECTRONICS`, `APPAREL`, `HOME`, `BEAUTY`, and `SPORTS`, with deterministic prices and starting stock. Give a few SKUs deliberately low stock, so out-of-stock failures and lost-revenue charts show up early in the demo. k6 reads the same product ID list.

## API

### `POST /inventory/reserve`

Decrements stock if enough is available. This call is in the checkout critical path.

Request:

```json
{ "product_id": "PROD_A", "quantity": 1 }
```

The decrement is a single statement. Postgres takes the row lock, and the `WHERE` clause makes the check-and-decrement atomic:

```sql
UPDATE inventory
   SET stock_level = stock_level - $2
 WHERE product_id = $1
   AND deleted_at IS NULL
   AND stock_level >= $2
RETURNING stock_level, price, category;
```

If zero rows are updated, the service runs a follow-up lookup to tell "unknown product" apart from "out of stock".

| Status | Body |
|---|---|
| `200 OK` | `{"product_id": "PROD_A", "quantity": 1, "unit_price": 49.99, "category": "APPAREL", "remaining_stock": 12}` |
| `400 Bad Request` | `{"error": "OUT_OF_STOCK", ...}` |
| `404 Not Found` | `{"error": "PRODUCT_NOT_FOUND", ...}` |

`unit_price` in the response is how the Order Service prices the order, since the checkout payload has no amount.

### `POST /inventory/release` (proposed)

Compensating action. The Order Service calls it when payment fails after a successful reservation.

```json
{ "product_id": "PROD_A", "quantity": 1 }
```

```sql
UPDATE inventory SET stock_level = stock_level + $2 WHERE product_id = $1;
```

Returns `200 OK` with `{"remaining_stock": 13}`.

> A reservation here is an immediate decrement, not a held reservation with an expiry. The design is simple and reliable for the demo. The trade-off: if the Order Service crashes between reserving and releasing, the stock leaks. The restock loop hides this over time.

### `GET /inventory/{product_id}`

Optional read endpoint for debugging and k6 catalog discovery. Returns the row.

### `POST /inventory/restock` (admin)

Optional hidden endpoint, so k6 can trigger replenishment bursts:

```json
{ "product_id": "PROD_A", "quantity": 100 }
```

## Background Restock Loop

So the simulation can run for a "virtual year", a ticker goroutine replenishes stock:

- Every `RESTOCK_INTERVAL` (for example 2 minutes), add `RESTOCK_QUANTITY` to every SKU below `RESTOCK_THRESHOLD`.
- Optionally, leave a small "chronically scarce" subset of SKUs out of automatic restocking, so lost-revenue stays visible.

Each restock is an `UPDATE`, so it bumps `updated_at`. The CDC sync carries those changes to StarRocks, and replenishment shows up as upward steps on the stock charts.

## Interactions

```mermaid
sequenceDiagram
  participant O as Order
  participant I as Inventory
  participant P as Payment
  O->>I: POST /inventory/reserve {PROD_A, 1}
  alt stock available
    I-->>O: 200 {unit_price: 49.99}
    O->>P: POST /payments/process
    alt declined
      O->>I: POST /inventory/release {PROD_A, 1}
    end
  else out of stock
    I-->>O: 400 OUT_OF_STOCK
    Note over O: order FAILED, payment never attempted
  end
```

## Configuration

| Variable | Example |
|---|---|
| `PORT` | `8083` |
| `DATABASE_URL` | `postgres://demo:demo@postgresql:5432/inventory?sslmode=disable&timezone=UTC` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |
| `OTEL_SERVICE_NAME` | `inventory-service` |
| `SEED_PRODUCTS` | `100` |
| `RESTOCK_INTERVAL` | `2m` |
| `RESTOCK_THRESHOLD` | `10` |
| `RESTOCK_QUANTITY` | `50` |

## Telemetry

- Inbound spans via `otelhttp.NewHandler`.
- Span attributes: `product.id`, `inventory.quantity`, `inventory.remaining_stock`. On failure, set span status to error with `inventory.failure=OUT_OF_STOCK`.
- The restock loop starts its own root span per run (`inventory.restock`), so replenishment is visible in Tempo too.

## Analytics Impact

For supply chain and operations, this is the most important table in StarRocks:

- **Stock depletion velocity:** the rate at which `stock_level` falls per SKU and category, from the stream of `updated_at` changes.
- **Lost revenue:** `orders WHERE failure_reason = 'OUT_OF_STOCK'` joined to `inventory.price`. This is how much the business would have made if popular items had stayed in stock.
- **Category mix:** revenue by `category` (`orders ⋈ inventory`).
