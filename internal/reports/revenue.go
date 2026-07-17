package reports

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"fahriddin-ai/internal/billz"
)

// MonthStats is one side of a period comparison (label left blank by
// ComparePeriods — callers that need a formatted label set it themselves,
// since the right format depends on what's being compared).
type MonthStats struct {
	Label        string  `json:"label"`
	Revenue      float64 `json:"revenue"`
	Transactions int     `json:"transactions"`
	Profit       float64 `json:"profit"`
}

// PeriodComparison is the result of comparing two arbitrary date ranges,
// optionally scoped to one shop.
type PeriodComparison struct {
	A             MonthStats `json:"this_month"`
	B             MonthStats `json:"last_year"`
	ChangePercent *float64   `json:"change_percent,omitempty"`
}

// ComparePeriods compares total revenue/transactions/profit between two
// arbitrary date ranges, company-wide or for a single shop. This is the
// general form of the "this month to date vs same range last year"
// comparison used throughout the app (see MonthToDateRange/
// SameRangeLastYear) — any two ranges work, so it also backs ad-hoc
// "compare period A to period B" questions asked of the AI agent.
func ComparePeriods(ctx context.Context, bz *billz.Client, shopID *string, aStart, aEnd, bStart, bEnd time.Time) (PeriodComparison, error) {
	aReport, err := bz.GeneralReport(ctx, aStart.Format("2006-01-02"), aEnd.Format("2006-01-02"))
	if err != nil {
		return PeriodComparison{}, err
	}
	bReport, err := bz.GeneralReport(ctx, bStart.Format("2006-01-02"), bEnd.Format("2006-01-02"))
	if err != nil {
		return PeriodComparison{}, err
	}

	a := statsFromReport(aReport, shopID)
	b := statsFromReport(bReport, shopID)

	cmp := PeriodComparison{A: a, B: b}
	if b.Revenue > 0 {
		pct := (a.Revenue - b.Revenue) / b.Revenue * 100
		cmp.ChangePercent = &pct
	}
	return cmp, nil
}

// statsFromReport pulls company-wide totals, or (if shopID is non-nil) just
// that shop's slice of the per-shop breakdown that GeneralReport always
// includes.
func statsFromReport(report *billz.GeneralReport, shopID *string) MonthStats {
	if shopID == nil {
		return MonthStats{
			Revenue:      report.GrossSales,
			Transactions: report.TransactionsCount,
			Profit:       report.GrossProfit,
		}
	}
	for _, st := range report.ShopStats {
		if st.ShopID == *shopID {
			return MonthStats{
				Revenue:      st.GrossSales,
				Transactions: st.TransactionsCount,
				Profit:       st.GrossProfit,
			}
		}
	}
	return MonthStats{}
}

// StoreDetailResponse is a single shop's daily revenue trend, its own
// year-over-year comparison, and its top-selling products/sellers.
type StoreDetailResponse struct {
	Name          string       `json:"name"`
	DailyTrend    []DailyPoint `json:"daily_trend"`
	ThisMonth     MonthStats   `json:"this_month"`
	LastYear      MonthStats   `json:"last_year"`
	ChangePercent *float64     `json:"change_percent,omitempty"`
	TopProducts   []ProductRow `json:"top_products"`
	TopSellers    []SellerRow  `json:"top_sellers"`
}

const (
	storeTrendDays   = 14
	storeTopProducts = 10
	storeTopSellers  = 8
)

// StoreDetail returns a single shop's daily revenue trend, its own
// year-over-year comparison, and its top-selling products/sellers. Not
// exposed as its own AI agent tool — the agent tool set covers the same
// ground compositionally via get_revenue_report/product_sales_report/
// seller_performance with a shop_id filter, which fits a conversational
// answer far better than one large blob. Only the Mini App's
// /api/analytics/store/{id} handler calls this directly.
func StoreDetail(ctx context.Context, bz *billz.Client, shopID string, now time.Time, loc *time.Location, log *slog.Logger) (StoreDetailResponse, error) {
	thisStart, thisEnd := MonthToDateRange(now)
	lastStart, lastEnd := SameRangeLastYear(now)
	// The daily table only needs to cover the trend chart + this month's
	// stats — the year-over-year comparison uses a separate lightweight
	// aggregate call below instead of pulling a full extra year of daily
	// rows just to sum them.
	trendStart := thisStart.AddDate(0, 0, -storeTrendDays)
	if trendStart.After(thisStart) {
		trendStart = thisStart
	}

	// The daily table, the product sales report, the seller-attributed
	// orders, and the year-ago aggregate are independent Billz calls — run
	// them concurrently instead of back-to-back so a slow network
	// round-trip only costs once, not four times.
	var (
		wg          sync.WaitGroup
		table       *billz.GeneralReportTable
		tableErr    error
		sales       *billz.ProductSalesReport
		salesErr    error
		orders      []billz.Order
		ordersErr   error
		lastYearRp  *billz.GeneralReport
		lastYearErr error
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		table, tableErr = bz.GeneralReportTable(ctx, trendStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"), []string{shopID}, "day", 1, 500)
	}()
	go func() {
		defer wg.Done()
		sales, salesErr = bz.ProductSales(ctx, thisStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"), []string{shopID}, 1, 200)
	}()
	go func() {
		defer wg.Done()
		orders, ordersErr = FetchAllOrders(ctx, bz, thisStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"), []string{shopID})
	}()
	go func() {
		defer wg.Done()
		lastYearRp, lastYearErr = bz.GeneralReport(ctx, lastStart.Format("2006-01-02"), lastEnd.Format("2006-01-02"))
	}()
	wg.Wait()

	if tableErr != nil {
		return StoreDetailResponse{}, tableErr
	}

	resp := StoreDetailResponse{}
	var thisMonth MonthStats
	type dayBucket struct {
		date time.Time
		stat billz.ShopDailyStat
	}
	var days []dayBucket
	for _, st := range table.ShopStatsByDate {
		if resp.Name == "" {
			resp.Name = st.ShopName
		}
		d, perr := time.ParseInLocation("2006-01-02 15:04:05", st.Date, loc)
		if perr != nil {
			continue
		}
		days = append(days, dayBucket{date: d, stat: st})

		if !d.Before(thisStart) && !d.After(thisEnd) {
			thisMonth.Revenue += st.GrossSales
			thisMonth.Transactions += st.TransactionsCount
			thisMonth.Profit += st.GrossProfit
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].date.Before(days[j].date) })

	start := 0
	if len(days) > storeTrendDays {
		start = len(days) - storeTrendDays
	}
	for _, db := range days[start:] {
		resp.DailyTrend = append(resp.DailyTrend, DailyPoint{
			Date:    db.date.Format("02.01"),
			Revenue: db.stat.GrossSales,
		})
	}

	thisMonth.Label = thisStart.Format("02.01") + " – " + thisEnd.Format("02.01")
	resp.ThisMonth = thisMonth

	if lastYearErr != nil {
		log.Warn("reports: store detail (last year) failed", "err", lastYearErr)
	} else {
		var shopStat billz.ShopStat
		for _, st := range lastYearRp.ShopStats {
			if st.ShopID == shopID {
				shopStat = st
				break
			}
		}
		lastYear := MonthStats{
			Label:        lastStart.Format("02.01.06") + " – " + lastEnd.Format("02.01.06"),
			Revenue:      shopStat.GrossSales,
			Transactions: shopStat.TransactionsCount,
			Profit:       shopStat.GrossProfit,
		}
		resp.LastYear = lastYear
		if lastYear.Revenue > 0 {
			pct := (thisMonth.Revenue - lastYear.Revenue) / lastYear.Revenue * 100
			resp.ChangePercent = &pct
		}
	}

	if salesErr != nil {
		log.Warn("reports: store detail (top products) failed", "err", salesErr)
	} else {
		byProduct := map[string]*ProductRow{}
		var order []string
		for _, p := range sales.Products {
			row, ok := byProduct[p.ProductID]
			if !ok {
				row = &ProductRow{Name: p.ProductName}
				byProduct[p.ProductID] = row
				order = append(order, p.ProductID)
			}
			row.Sold += p.SoldMeasurementValue
			row.Revenue += p.GrossSales
		}
		top := make([]ProductRow, 0, len(order))
		for _, id := range order {
			top = append(top, *byProduct[id])
		}
		sort.Slice(top, func(i, j int) bool { return top[i].Revenue > top[j].Revenue })
		if len(top) > storeTopProducts {
			top = top[:storeTopProducts]
		}
		resp.TopProducts = top
	}

	if ordersErr != nil {
		log.Warn("reports: store detail (top sellers) failed", "err", ordersErr)
	} else {
		resp.TopSellers = TopSellers(AggregateSellersAll(orders), storeTopSellers)
	}

	return resp, nil
}

const (
	forecastHistoryDays = 60
	forecastTrendWindow = 14
)

// ForecastResponse projects the current month's final revenue.
type ForecastResponse struct {
	MonthToDateRevenue float64  `json:"month_to_date_revenue"`
	ProjectedTotal     float64  `json:"projected_total"`
	DaysElapsed        int      `json:"days_elapsed"`
	DaysRemaining      int      `json:"days_remaining"`
	LastYearTotal      float64  `json:"last_year_total,omitempty"`
	ChangePercent      *float64 `json:"change_percent,omitempty"`
}

// Forecast projects this month's final revenue from what's already sold
// plus an estimate for the remaining days. The estimate for each remaining
// day is that day-of-week's historical average (60-day window, so it
// reflects each weekday's typical pace rather than a flat daily average)
// scaled by a recent trend multiplier (last 14 days vs the 14 days before
// that) — a simple model, but it accounts for the two variables that
// matter most for a retail chain: weekly seasonality and whether the
// business is currently trending up or down. Always projects the current
// month (no date-range params) — that's the only thing "forecast" means.
func Forecast(ctx context.Context, bz *billz.Client, shopIDs []string, now time.Time, loc *time.Location, log *slog.Logger) (ForecastResponse, error) {
	histStart := now.AddDate(0, 0, -forecastHistoryDays)
	rows, err := FetchAllGeneralReportTable(ctx, bz, histStart.Format("2006-01-02"), now.Format("2006-01-02"), shopIDs, "day")
	if err != nil {
		return ForecastResponse{}, err
	}

	dailyTotals := map[string]float64{}
	for _, row := range rows {
		d, perr := time.ParseInLocation("2006-01-02 15:04:05", row.Date, loc)
		if perr != nil {
			continue
		}
		dailyTotals[d.Format("2006-01-02")] += row.GrossSales
	}

	var weekdaySum [7]float64
	var weekdayCount [7]int
	for dateStr, rev := range dailyTotals {
		d, perr := time.ParseInLocation("2006-01-02", dateStr, loc)
		if perr != nil {
			continue
		}
		wd := int(d.Weekday())
		weekdaySum[wd] += rev
		weekdayCount[wd]++
	}
	var weekdayAvg [7]float64
	for i := 0; i < 7; i++ {
		if weekdayCount[i] > 0 {
			weekdayAvg[i] = weekdaySum[i] / float64(weekdayCount[i])
		}
	}

	trend := forecastTrendMultiplier(dailyTotals, now)

	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	monthEnd := monthStart.AddDate(0, 1, -1)

	var monthToDate float64
	for d := monthStart; !d.After(now); d = d.AddDate(0, 0, 1) {
		monthToDate += dailyTotals[d.Format("2006-01-02")]
	}

	projected := monthToDate
	for d := now.AddDate(0, 0, 1); !d.After(monthEnd); d = d.AddDate(0, 0, 1) {
		projected += weekdayAvg[int(d.Weekday())] * trend
	}

	resp := ForecastResponse{
		MonthToDateRevenue: monthToDate,
		ProjectedTotal:     projected,
		DaysElapsed:        now.Day(),
		DaysRemaining:      monthEnd.Day() - now.Day(),
	}

	// Same calendar month, one year back — the full month's actual total,
	// since the forecast projects to this month's end. Consistent with
	// every other comparison in the app: year-over-year, not month-to-month.
	lastYearMonthStart := time.Date(now.Year()-1, now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastYearMonthEnd := lastYearMonthStart.AddDate(0, 1, -1)
	if lastYearReport, err := bz.GeneralReport(ctx, lastYearMonthStart.Format("2006-01-02"), lastYearMonthEnd.Format("2006-01-02")); err != nil {
		log.Warn("reports: forecast last year report failed", "err", err)
	} else if lastYearReport.GrossSales > 0 {
		resp.LastYearTotal = lastYearReport.GrossSales
		pct := (projected - lastYearReport.GrossSales) / lastYearReport.GrossSales * 100
		resp.ChangePercent = &pct
	}

	return resp, nil
}

// forecastTrendMultiplier compares the last 14 days' average daily revenue
// against the 14 days before that, clamped to [0.5, 2.0] so a noisy short
// window can't blow up the projection.
func forecastTrendMultiplier(dailyTotals map[string]float64, now time.Time) float64 {
	var recentSum, priorSum float64
	for i := 1; i <= forecastTrendWindow; i++ {
		recentSum += dailyTotals[now.AddDate(0, 0, -i).Format("2006-01-02")]
	}
	for i := forecastTrendWindow + 1; i <= forecastTrendWindow*2; i++ {
		priorSum += dailyTotals[now.AddDate(0, 0, -i).Format("2006-01-02")]
	}
	if priorSum <= 0 {
		return 1.0
	}
	mult := recentSum / priorSum
	if mult < 0.5 {
		return 0.5
	}
	if mult > 2.0 {
		return 2.0
	}
	return mult
}
