DROP TABLE IF EXISTS stock_by_shop;

ALTER TABLE vmi_summary
    DROP COLUMN IF EXISTS total_stock_quantity,
    DROP COLUMN IF EXISTS total_stock_value;
