-- +goose Up
CREATE TABLE customers (
    id           BIGSERIAL    PRIMARY KEY,
    email        VARCHAR(255) NOT NULL,
    country      VARCHAR(2)   NOT NULL, -- ISO 3166-1 alpha-2
    loyalty_tier VARCHAR(20)  NOT NULL CHECK (loyalty_tier IN ('BRONZE', 'SILVER', 'GOLD')),
    status       VARCHAR(20)  NOT NULL DEFAULT 'ACTIVE'
                              CHECK (status IN ('ACTIVE', 'SUSPENDED', 'PENDING_DELETION')),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

-- Unique among live customers only, so the email of a soft-deleted customer
-- can register again.
CREATE UNIQUE INDEX customers_email_live_uq ON customers (email) WHERE deleted_at IS NULL;

-- Cursor index for the CDC sync daemon: WHERE (updated_at, id) > ($1, $2).
CREATE INDEX customers_updated_at_id_idx ON customers (updated_at, id);

CREATE TRIGGER customers_set_updated_at
    BEFORE UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE customers;
