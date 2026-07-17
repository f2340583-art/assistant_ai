package reports

import (
	"context"
	"database/sql"
	"strings"

	"fahriddin-ai/internal/billz"
	"fahriddin-ai/internal/scheduler"
)

const stockPageLimit = 500

// StockByShop returns current stock for a single shop, optionally filtered
// by a product name/SKU substring, from a live Billz call (today's report
// date). Capped at stockPageLimit rows plus one extra page to catch a
// filter match near the boundary — a per-shop catalog is small enough that
// a live call is fine, unlike a company-wide pull (see StockCompanyWide).
func StockByShop(ctx context.Context, bz *billz.Client, shopID, productQuery string, today string) ([]billz.StockRow, error) {
	report, err := bz.StockLevels(ctx, today, []string{shopID}, 1, stockPageLimit)
	if err != nil {
		return nil, err
	}
	if productQuery == "" {
		return report.Rows, nil
	}
	q := strings.ToLower(productQuery)
	var filtered []billz.StockRow
	for _, row := range report.Rows {
		if strings.Contains(strings.ToLower(row.ProductName), q) || strings.Contains(strings.ToLower(row.ProductSKU), q) {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

// StockCompanyWide answers company-wide/product-name stock questions from
// the cached VMI report (refreshed every 6h by the scheduler) instead of a
// live Billz call — a live full-catalog pull is far too slow for a chat
// reply (the VMI job itself pages through the entire ~26k-product catalog).
// Only products the VMI job tracks (recent sellers) are covered; a
// never-selling product won't show up here.
func StockCompanyWide(ctx context.Context, db *sql.DB, productQuery string) ([]scheduler.VMIItem, error) {
	all, err := scheduler.VMIReport(ctx, db, "")
	if err != nil {
		return nil, err
	}
	if productQuery == "" {
		return all, nil
	}
	q := strings.ToLower(productQuery)
	var filtered []scheduler.VMIItem
	for _, it := range all {
		if strings.Contains(strings.ToLower(it.ProductName), q) || strings.Contains(strings.ToLower(it.SKU), q) {
			filtered = append(filtered, it)
		}
	}
	return filtered, nil
}
