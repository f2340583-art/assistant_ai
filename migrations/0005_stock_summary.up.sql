ALTER TABLE vmi_summary
    ADD COLUMN total_stock_quantity NUMERIC NOT NULL DEFAULT 0,
    ADD COLUMN total_stock_value    NUMERIC NOT NULL DEFAULT 0;

-- Full-catalog stock, broken down per shop — computed alongside the VMI
-- job (which already pages through the full stock-report-table), so this
-- costs no extra Billz calls.
CREATE TABLE stock_by_shop (
    shop_id        TEXT PRIMARY KEY,
    shop_name      TEXT NOT NULL,
    total_quantity NUMERIC NOT NULL,
    total_value    NUMERIC NOT NULL,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
