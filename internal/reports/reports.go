// Package reports holds pure, HTTP-agnostic Billz report/composition logic.
// Both internal/webapp (Mini App JSON API) and internal/agent (Telegram
// tool-use agent) call these same functions instead of duplicating the
// aggregation logic in two places.
package reports

import (
	"context"
	"time"

	"fahriddin-ai/internal/billz"
)

const (
	orderPageLimit   = 200
	orderMaxPages    = 60
	productPageLimit = 1000
	productMaxPages  = 50
	tablePageLimit   = 1000
	tableMaxPages    = 20
)

// DailyPoint is one point on a revenue trend chart.
type DailyPoint struct {
	Date    string  `json:"date"`
	Revenue float64 `json:"revenue"`
}

// ProductRow is one product's sales within a period.
type ProductRow struct {
	Name    string  `json:"name"`
	Sold    float64 `json:"sold"`
	Revenue float64 `json:"revenue"`
}

// SellerRow is one seller's attributed sales within a period.
type SellerRow struct {
	Name      string  `json:"name"`
	Revenue   float64 `json:"revenue"`
	ItemsSold int     `json:"items_sold"`
}

// CategoryShare is one product category's revenue share within a period.
type CategoryShare struct {
	Name    string  `json:"name"`
	Revenue float64 `json:"revenue"`
	Percent float64 `json:"percent"`
}

// PaymentShare is one payment method's revenue share within a period.
type PaymentShare struct {
	Name    string  `json:"name"`
	Sum     float64 `json:"sum"`
	Percent float64 `json:"percent"`
}

const SellerUnknown = "Noma'lum"

// FetchAllOrders pages through GET /v3/order-search for the given shops and
// date range. The response's top-level "count" doesn't reliably reflect the
// shop_ids filter, so pagination stops when a page returns fewer orders than
// the requested limit rather than trusting count.
func FetchAllOrders(ctx context.Context, bz *billz.Client, startDate, endDate string, shopIDs []string) ([]billz.Order, error) {
	var all []billz.Order
	for page := 1; page <= orderMaxPages; page++ {
		report, err := bz.OrderSearch(ctx, startDate, endDate, shopIDs, page, orderPageLimit)
		if err != nil {
			return nil, err
		}
		pageCount := 0
		for _, bucket := range report.OrdersByDate {
			all = append(all, bucket.Orders...)
			pageCount += len(bucket.Orders)
		}
		if pageCount < orderPageLimit {
			break
		}
	}
	return all, nil
}

// FetchAllProductSales pages through GET /v1/product-general-table for the
// given shops and date range.
func FetchAllProductSales(ctx context.Context, bz *billz.Client, startDate, endDate string, shopIDs []string) ([]billz.ProductSale, error) {
	var all []billz.ProductSale
	for page := 1; page <= productMaxPages; page++ {
		report, err := bz.ProductSales(ctx, startDate, endDate, shopIDs, page, productPageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, report.Products...)
		if len(report.Products) < productPageLimit {
			break
		}
	}
	return all, nil
}

// FetchAllGeneralReportTable pages through GET /v1/general-report-table for
// the given shops, date range, and bucketing (day/week/month/year).
func FetchAllGeneralReportTable(ctx context.Context, bz *billz.Client, startDate, endDate string, shopIDs []string, detalization string) ([]billz.ShopDailyStat, error) {
	var all []billz.ShopDailyStat
	for page := 1; page <= tableMaxPages; page++ {
		table, err := bz.GeneralReportTable(ctx, startDate, endDate, shopIDs, detalization, page, tablePageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, table.ShopStatsByDate...)
		if len(table.ShopStatsByDate) < tablePageLimit {
			break
		}
	}
	return all, nil
}

// ShopIDs fetches today's shop list — Billz's product/stock/table endpoints
// require an explicit shop_ids list (unlike GeneralReport, which defaults to
// "all shops"), so any report that needs "all shops" borrows it from a
// cheap GeneralReport call instead of hardcoding shop IDs.
func ShopIDs(ctx context.Context, bz *billz.Client, now time.Time) ([]string, error) {
	today := now.Format("2006-01-02")
	report, err := bz.GeneralReport(ctx, today, today)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(report.ShopStats))
	for _, st := range report.ShopStats {
		ids = append(ids, st.ShopID)
	}
	return ids, nil
}

// MonthToDateRange returns the start of the current calendar month through
// now.
func MonthToDateRange(now time.Time) (start, end time.Time) {
	start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return start, now
}

// SameRangeLastYear returns the same month, same day-of-month range, one
// year back — the fair baseline for a seasonal retail business (unlike last
// month, which conflates seasonality with real growth/decline).
func SameRangeLastYear(now time.Time) (start, end time.Time) {
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastYearStart := thisMonthStart.AddDate(-1, 0, 0)
	daysIntoMonth := now.Day() - 1
	lastYearEnd := lastYearStart.AddDate(0, 0, daysIntoMonth)
	return lastYearStart, lastYearEnd
}

// OneYearBack shifts an arbitrary date range back exactly one year —
// the generalized version of SameRangeLastYear for periods that aren't
// necessarily month-to-date (used by SellerYoY).
func OneYearBack(start, end time.Time) (time.Time, time.Time) {
	return start.AddDate(-1, 0, 0), end.AddDate(-1, 0, 0)
}
