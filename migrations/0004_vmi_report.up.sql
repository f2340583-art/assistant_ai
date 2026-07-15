DROP TABLE IF EXISTS stockout_report;

CREATE TABLE vmi_report (
    product_id                   TEXT PRIMARY KEY,
    product_name                 TEXT NOT NULL,
    sku                          TEXT NOT NULL,
    category                     TEXT NOT NULL, -- 'dead', 'low', 'out', 'top'
    total_stock                  NUMERIC NOT NULL,
    retail_price                 NUMERIC,
    daily_velocity               NUMERIC,
    days_remaining               NUMERIC,
    profit_per_unit              NUMERIC,
    potential_daily_profit_loss  NUMERIC,
    frozen_value                 NUMERIC,
    gross_sales_30d              NUMERIC NOT NULL DEFAULT 0,
    net_profit_30d               NUMERIC NOT NULL DEFAULT 0,
    computed_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX vmi_report_category_idx ON vmi_report (category);

-- Single-row table: full-catalog totals computed before the display table is
-- trimmed to its top-N per category, so "how much money is frozen" reflects
-- every dead SKU, not just the ones shown in the list.
CREATE TABLE vmi_summary (
    id                                SMALLINT PRIMARY KEY DEFAULT 1,
    total_frozen_capital             NUMERIC NOT NULL,
    dead_sku_count                   INT NOT NULL,
    total_potential_daily_profit_loss NUMERIC NOT NULL,
    at_risk_sku_count                INT NOT NULL,
    computed_at                       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = 1)
);
