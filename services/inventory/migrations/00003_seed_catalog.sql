-- Deterministic demo catalog: PROD_0001 .. PROD_0100, the same in every
-- environment, so k6 scripts and dashboards can rely on the IDs.
--
--   * Categories rotate through five values.
--   * Prices fall in a realistic band per category and end in .99.
--   * Every 20th SKU starts with only 3 units, so out-of-stock failures appear
--     within minutes of starting load.
--   * Every 25th SKU is never auto-restocked, so it stays chronically scarce.
--     PROD_0100 is both: it sells out quickly and stays out.

-- +goose Up
WITH catalog AS (
    SELECT
        i,
        (ARRAY['ELECTRONICS', 'APPAREL', 'HOME', 'BEAUTY', 'SPORTS'])[1 + (i - 1) % 5] AS category,
        -- A spread that looks random but is reproducible.
        (i * 7919) % 100 AS spread
    FROM generate_series(1, 100) AS i
)
INSERT INTO inventory (product_id, category, stock_level, price, auto_restock)
SELECT
    'PROD_' || lpad(i::text, 4, '0'),
    category,
    CASE WHEN i % 20 = 0 THEN 3 ELSE 40 + (i * 37) % 160 END,
    CASE category
        WHEN 'ELECTRONICS' THEN 99 + spread * 8
        WHEN 'APPAREL'     THEN 19 + spread
        WHEN 'HOME'        THEN 29 + spread * 3
        WHEN 'BEAUTY'      THEN 9  + spread / 2
        WHEN 'SPORTS'      THEN 24 + spread * 2
    END + 0.99,
    i % 25 <> 0
FROM catalog;

-- +goose Down
DELETE FROM inventory WHERE product_id ~ '^PROD_[0-9]{4}$';
