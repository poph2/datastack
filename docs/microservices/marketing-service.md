# Marketing Service: Top-of-Funnel Attribution

| | |
|---|---|
| Port | `8082` |
| Kubernetes service | `marketing-service` |
| Database | `marketing` |
| Tables | `campaigns`, `attributions` |
| Called by | k6, Order Service |
| Calls | none (leaf node) |

## Purpose

The Marketing Service sits at the very edge of the architecture. It connects anonymous traffic to paying customers by recording where a visitor came from, before they add anything to a cart. Later it links that click to the purchase it produced.

## Responsibilities

- **Touchpoint logging.** Records each incoming visit's traffic source (UTM parameters such as `utm_source=facebook`, `utm_campaign=summer_sale`) and issues a unique `attribution_id` for it.
- **Campaign management.** Keeps the list of active campaigns and their ad spend. This is the cost baseline for ROAS and CAC.
- **Conversion handshake.** Waits for a conversion. When the Order Service reports a successful checkout, the service attaches the `order_id` to the original attribution, closing the loop from click to purchase.

## Data Model

Schema as specified:

```sql
CREATE TABLE campaigns (
    id SERIAL PRIMARY KEY,
    utm_source VARCHAR(100),
    utm_campaign VARCHAR(100),
    ad_spend DECIMAL(10,2),
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE attributions (
    id SERIAL PRIMARY KEY,
    user_id INT,
    order_id INT, -- Populated later when the order completes
    utm_source VARCHAR(100),
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TRIGGER campaigns_set_updated_at
  BEFORE UPDATE ON campaigns FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER attributions_set_updated_at
  BEFORE UPDATE ON attributions FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

`user_id` and `order_id` are logical references to other services' data. They are **not** foreign keys, because those rows live in other databases.

### Proposed additions

```sql
ALTER TABLE attributions ADD COLUMN utm_campaign VARCHAR(100);   -- join to campaigns at campaign granularity
ALTER TABLE attributions ADD COLUMN order_amount DECIMAL(10,2);  -- revenue reported at conversion
ALTER TABLE attributions ADD COLUMN converted_at TIMESTAMP;      -- click-to-purchase latency
```

- Without `utm_campaign`, ROAS can only be computed per source, not per campaign.
- `order_amount` keeps the "user spent $150 on order 999" fact on the marketing side. Dashboards can still use `payments` as the authoritative cleared amount.

### Seed data

At startup the service upserts a fixed campaign catalog so k6 and dashboards have stable keys:

| utm_source | utm_campaign | ad_spend |
|---|---|---|
| `google` | `search_brand` | 1200.00 |
| `facebook` | `summer_sale` | 800.00 |
| `instagram` | `influencer_q3` | 650.00 |
| `tiktok` | `viral_launch` | 400.00 |
| `email` | `newsletter` | 0.00 |
| `organic` | `none` | 0.00 |

Optionally, a background job can increase `ad_spend` over time (for example daily spend accrual), so cost moves alongside revenue in the dashboards.

## API

### `POST /attributions`

Logs an ad click or visit. k6 calls this at the start of each user journey and carries the returned ID into checkout.

Request:

```json
{ "user_id": 1234, "utm_source": "facebook", "utm_campaign": "summer_sale" }
```

| Status | Body |
|---|---|
| `201 Created` | `{"attribution_id": 99}` |
| `400 Bad Request` | `{"error": "INVALID_REQUEST", ...}` (missing `user_id` or `utm_source`) |

### `PUT /attributions/{id}/order`

Attaches the conversion. The Order Service calls this after payment succeeds.

Request:

```json
{ "order_id": 999, "order_amount": 150.00 }
```

| Status | Body |
|---|---|
| `200 OK` | `{"attribution_id": 99, "order_id": 999}` |
| `404 Not Found` | `{"error": "ATTRIBUTION_NOT_FOUND", ...}` |
| `409 Conflict` | `{"error": "ALREADY_CONVERTED", ...}`: the attribution already has a different `order_id` |

The call is **idempotent**: sending the same `order_id` again returns `200`, so the Order Service can retry safely.

## Interactions

```mermaid
sequenceDiagram
  participant k6
  participant M as Marketing
  participant O as Order
  k6->>M: POST /attributions {user_id, utm_source}
  M-->>k6: {attribution_id: 99}
  k6->>O: POST /orders/checkout {..., attribution_id: 99}
  Note over O: reserve, pay...
  O->>M: PUT /attributions/99/order {order_id: 999, order_amount: 150}
  M-->>O: 200 OK
```

- **Upstream, k6:** creates touchpoints.
- **Upstream, Order Service:** reports conversions. This is **not** in the checkout critical path. If the Marketing Service is down or slow, the checkout still succeeds and the attribution stays unconverted. The Order Service makes this call best-effort with a short timeout. It can also run it in a background goroutine with a detached context, keeping the trace link.

## Configuration

| Variable | Example |
|---|---|
| `PORT` | `8082` |
| `DATABASE_URL` | `postgres://demo:demo@postgresql:5432/marketing?sslmode=disable&timezone=UTC` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |
| `OTEL_SERVICE_NAME` | `marketing-service` |
| `SEED_CAMPAIGNS` | `true` |

## Telemetry

- Inbound spans via `otelhttp.NewHandler`.
- Span attributes: `attribution.id`, `attribution.utm_source`, `order.id` (on conversion).

## Analytics Impact

This service makes Grafana useful to a CMO. Joining attribution to cleared payments shows that, say, $6,000 of today's $10,000 came from Facebook at $500 of spend, and $4,000 came from the newsletter at $0.

- **Campaign ROI / ROAS:** `SUM(payments.amount WHERE status='SUCCESS') / campaigns.ad_spend`, grouped by source or campaign.
- **CAC:** `ad_spend / COUNT(DISTINCT converted user_id)`.
- **Conversion rate:** converted attributions divided by all attributions, per source.
- **Click-to-purchase latency:** `converted_at - created_at`.
