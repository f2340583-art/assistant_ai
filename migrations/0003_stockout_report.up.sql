CREATE TABLE stockout_report (
    product_id     TEXT PRIMARY KEY,
    product_name   TEXT NOT NULL,
    sku            TEXT NOT NULL,
    total_stock    NUMERIC NOT NULL,
    daily_velocity NUMERIC NOT NULL,
    days_remaining NUMERIC,
    status         TEXT NOT NULL, -- 'out' or 'low'
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
