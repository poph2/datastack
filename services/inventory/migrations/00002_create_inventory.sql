-- +goose Up
CREATE TABLE inventory (
    id           BIGSERIAL     PRIMARY KEY,
    product_id   VARCHAR(50)   NOT NULL,
    category     VARCHAR(50)   NOT NULL,
    -- The CHECK is a backstop. Reservations already refuse to go below zero,
    -- but a bug must never be able to sell stock that doesn't exist.
    stock_level  INT           NOT NULL CHECK (stock_level >= 0),
    price        DECIMAL(10,2) NOT NULL CHECK (price > 0),
    -- false keeps a SKU out of the background restock, so it stays scarce
    -- and lost-revenue dashboards have something to show.
    auto_restock BOOLEAN       NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

-- Unique among live products only (see the soft-delete convention).
CREATE UNIQUE INDEX inventory_product_id_live_uq ON inventory (product_id) WHERE deleted_at IS NULL;

-- Cursor index for the CDC sync daemon: WHERE (updated_at, id) > ($1, $2).
CREATE INDEX inventory_updated_at_id_idx ON inventory (updated_at, id);

CREATE TRIGGER inventory_set_updated_at
    BEFORE UPDATE ON inventory
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE inventory;
