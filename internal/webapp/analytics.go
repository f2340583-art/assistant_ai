package webapp

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"fahriddin-ai/internal/billz"
	"fahriddin-ai/internal/scheduler"
)

type monthStats struct {
	Label        string  `json:"label"`
	Revenue      float64 `json:"revenue"`
	Transactions int     `json:"transactions"`
	Profit       float64 `json:"profit"`
}

type monthlyComparisonResponse struct {
	ThisMonth     monthStats `json:"this_month"`
	LastYear      monthStats `json:"last_year"`
	ChangePercent *float64   `json:"change_percent,omitempty"`
}

// handleAnalyticsMonthly compares revenue "start of this month to today"
// against the same calendar range one year ago — not last month, which for
// a seasonal retail business is an apples-to-oranges comparison (e.g.
// December vs November). Same month, one year back is the fair baseline.
func (s *Server) handleAnalyticsMonthly(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, monthlyComparisonResponse{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)

	thisStart, thisEnd := monthToDateRange(now)
	lastStart, lastEnd := sameRangeLastYear(now)

	thisReport, err := s.billz.GeneralReport(ctx, thisStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"))
	if err != nil {
		s.log.Error("webapp: billz monthly report (this month) failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	lastReport, err := s.billz.GeneralReport(ctx, lastStart.Format("2006-01-02"), lastEnd.Format("2006-01-02"))
	if err != nil {
		s.log.Error("webapp: billz monthly report (last year) failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := monthlyComparisonResponse{
		ThisMonth: monthStats{
			Label:        thisStart.Format("02.01") + " – " + thisEnd.Format("02.01"),
			Revenue:      thisReport.GrossSales,
			Transactions: thisReport.TransactionsCount,
			Profit:       thisReport.GrossProfit,
		},
		LastYear: monthStats{
			Label:        lastStart.Format("02.01.06") + " – " + lastEnd.Format("02.01.06"),
			Revenue:      lastReport.GrossSales,
			Transactions: lastReport.TransactionsCount,
			Profit:       lastReport.GrossProfit,
		},
	}
	if lastReport.GrossSales > 0 {
		pct := (thisReport.GrossSales - lastReport.GrossSales) / lastReport.GrossSales * 100
		resp.ChangePercent = &pct
	}
	writeJSON(w, http.StatusOK, resp)
}

type dailyPoint struct {
	Date    string  `json:"date"`
	Revenue float64 `json:"revenue"`
}

type productRow struct {
	Name    string  `json:"name"`
	Sold    float64 `json:"sold"`
	Revenue float64 `json:"revenue"`
}

type sellerRow struct {
	Name      string  `json:"name"`
	Revenue   float64 `json:"revenue"`
	ItemsSold int     `json:"items_sold"`
}

type storeDetailResponse struct {
	Name          string       `json:"name"`
	DailyTrend    []dailyPoint `json:"daily_trend"`
	ThisMonth     monthStats   `json:"this_month"`
	LastYear      monthStats   `json:"last_year"`
	ChangePercent *float64     `json:"change_percent,omitempty"`
	TopProducts   []productRow `json:"top_products"`
	TopSellers    []sellerRow  `json:"top_sellers"`
}

// handleStoreDetail returns a single shop's daily revenue trend, its own
// year-over-year comparison, and its top-selling products.
func (s *Server) handleStoreDetail(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		http.Error(w, "billz not configured", http.StatusNotFound)
		return
	}
	shopID := r.PathValue("id")
	if shopID == "" {
		http.Error(w, "missing shop id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	now := time.Now().In(s.loc)
	thisStart, thisEnd := monthToDateRange(now)
	lastStart, lastEnd := sameRangeLastYear(now)
	// The daily table only needs to cover the trend chart + this month's
	// stats — the year-over-year comparison uses a separate lightweight
	// aggregate call below instead of pulling a full extra year of daily
	// rows just to sum them.
	trendStart := thisStart.AddDate(0, 0, -14)
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
		table, tableErr = s.billz.GeneralReportTable(ctx, trendStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"), []string{shopID}, "day", 1, 500)
	}()
	go func() {
		defer wg.Done()
		sales, salesErr = s.billz.ProductSales(ctx, thisStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"), []string{shopID}, 1, 200)
	}()
	go func() {
		defer wg.Done()
		orders, ordersErr = s.fetchAllOrders(ctx, thisStart.Format("2006-01-02"), thisEnd.Format("2006-01-02"), []string{shopID})
	}()
	go func() {
		defer wg.Done()
		lastYearRp, lastYearErr = s.billz.GeneralReport(ctx, lastStart.Format("2006-01-02"), lastEnd.Format("2006-01-02"))
	}()
	wg.Wait()

	if tableErr != nil {
		s.log.Error("webapp: billz store detail (daily table) failed", "err", tableErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := storeDetailResponse{}
	var thisMonth monthStats
	type dayBucket struct {
		date time.Time
		stat billz.ShopDailyStat
	}
	var days []dayBucket
	for _, st := range table.ShopStatsByDate {
		if resp.Name == "" {
			resp.Name = st.ShopName
		}
		d, perr := time.ParseInLocation("2006-01-02 15:04:05", st.Date, s.loc)
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

	const trendDays = 14
	start := 0
	if len(days) > trendDays {
		start = len(days) - trendDays
	}
	for _, db := range days[start:] {
		resp.DailyTrend = append(resp.DailyTrend, dailyPoint{
			Date:    db.date.Format("02.01"),
			Revenue: db.stat.GrossSales,
		})
	}

	thisMonth.Label = thisStart.Format("02.01") + " – " + thisEnd.Format("02.01")
	resp.ThisMonth = thisMonth

	if lastYearErr != nil {
		s.log.Warn("webapp: billz store detail (last year) failed", "err", lastYearErr)
	} else {
		// GeneralReport always returns company-wide totals with a per-shop
		// breakdown — pull out just this shop's slice, not the whole company.
		var shopStat billz.ShopStat
		for _, st := range lastYearRp.ShopStats {
			if st.ShopID == shopID {
				shopStat = st
				break
			}
		}
		lastYear := monthStats{
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
		s.log.Warn("webapp: billz store detail (top products) failed", "err", salesErr)
	} else {
		byProduct := map[string]*productRow{}
		var order []string
		for _, p := range sales.Products {
			row, ok := byProduct[p.ProductID]
			if !ok {
				row = &productRow{Name: p.ProductName}
				byProduct[p.ProductID] = row
				order = append(order, p.ProductID)
			}
			row.Sold += p.SoldMeasurementValue
			row.Revenue += p.GrossSales
		}
		top := make([]productRow, 0, len(order))
		for _, id := range order {
			top = append(top, *byProduct[id])
		}
		sort.Slice(top, func(i, j int) bool { return top[i].Revenue > top[j].Revenue })
		if len(top) > 10 {
			top = top[:10]
		}
		resp.TopProducts = top
	}

	if ordersErr != nil {
		s.log.Warn("webapp: billz store detail (top sellers) failed", "err", ordersErr)
	} else {
		resp.TopSellers = aggregateSellers(orders, sellerTopN)
	}

	writeJSON(w, http.StatusOK, resp)
}

const (
	sellerPageLimit = 200
	sellerMaxPages  = 60
	sellerTopN      = 8
	sellerUnknown   = "Noma'lum"
)

// fetchAllOrders pages through GET /v3/order-search for a single shop and
// date range. The response's top-level "count" doesn't reliably reflect the
// shop_ids filter, so pagination stops when a page returns fewer orders than
// the requested limit rather than trusting count.
func (s *Server) fetchAllOrders(ctx context.Context, startDate, endDate string, shopIDs []string) ([]billz.Order, error) {
	var all []billz.Order
	for page := 1; page <= sellerMaxPages; page++ {
		report, err := s.billz.OrderSearch(ctx, startDate, endDate, shopIDs, page, sellerPageLimit)
		if err != nil {
			return nil, err
		}
		pageCount := 0
		for _, bucket := range report.OrdersByDate {
			all = append(all, bucket.Orders...)
			pageCount += len(bucket.Orders)
		}
		if pageCount < sellerPageLimit {
			break
		}
	}
	return all, nil
}

// aggregateSellers sums each line item's revenue by the seller attributed to
// it (an order item can in principle list more than one seller — Billz's own
// UI treats the first as primary, so we do too). Items with no seller
// recorded are folded into a "Noma'lum" bucket rather than silently dropped,
// so the total still reconciles with the store's real revenue.
func aggregateSellers(orders []billz.Order, topN int) []sellerRow {
	byName := map[string]*sellerRow{}
	var order []string
	for _, o := range orders {
		for _, item := range o.Detail.Items {
			name := sellerUnknown
			if len(item.Sellers) > 0 && item.Sellers[0].Seller.Name != "" {
				name = item.Sellers[0].Seller.Name
			}
			row, ok := byName[name]
			if !ok {
				row = &sellerRow{Name: name}
				byName[name] = row
				order = append(order, name)
			}
			row.Revenue += item.TotalPrice
			row.ItemsSold++
		}
	}

	result := make([]sellerRow, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Revenue > result[j].Revenue })
	if len(result) > topN {
		result = result[:topN]
	}
	return result
}

const employeeTopN = 50

// handleEmployees is the company-wide counterpart to handleStoreDetail's
// per-shop seller breakdown — same order-search + aggregateSellers pipeline,
// just given every shop ID instead of one.
func (s *Server) handleEmployees(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, []sellerRow{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)
	monthStart, monthEnd := monthToDateRange(now)

	shopIDs, err := s.shopIDs(ctx, now)
	if err != nil {
		s.log.Error("webapp: employees: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, []sellerRow{})
		return
	}

	orders, err := s.fetchAllOrders(ctx, monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02"), shopIDs)
	if err != nil {
		s.log.Error("webapp: employees: failed to fetch orders", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, aggregateSellers(orders, employeeTopN))
}

type vmiItemResponse struct {
	ProductName              string   `json:"product_name"`
	SKU                      string   `json:"sku"`
	Category                 string   `json:"category"`
	TotalStock               float64  `json:"total_stock"`
	RetailPrice              *float64 `json:"retail_price,omitempty"`
	DailyVelocity            *float64 `json:"daily_velocity,omitempty"`
	DaysRemaining            *float64 `json:"days_remaining,omitempty"`
	ProfitPerUnit            *float64 `json:"profit_per_unit,omitempty"`
	PotentialDailyProfitLoss *float64 `json:"potential_daily_profit_loss,omitempty"`
	FrozenValue              *float64 `json:"frozen_value,omitempty"`
	GrossSales30d            float64  `json:"gross_sales_30d"`
	NetProfit30d             float64  `json:"net_profit_30d"`
}

type vmiSummaryResponse struct {
	TotalFrozenCapital            float64 `json:"total_frozen_capital"`
	DeadSKUCount                  int     `json:"dead_sku_count"`
	TotalPotentialDailyProfitLoss float64 `json:"total_potential_daily_profit_loss"`
	AtRiskSKUCount                int     `json:"at_risk_sku_count"`
}

type vmiResponse struct {
	Summary    vmiSummaryResponse `json:"summary"`
	DeadStock  []vmiItemResponse  `json:"dead_stock"`
	AtRisk     []vmiItemResponse  `json:"at_risk"`
	TopSellers []vmiItemResponse  `json:"top_sellers"`
}

func toVMIItemResponse(it scheduler.VMIItem) vmiItemResponse {
	return vmiItemResponse{
		ProductName:              it.ProductName,
		SKU:                      it.SKU,
		Category:                 it.Category,
		TotalStock:               it.TotalStock,
		RetailPrice:              it.RetailPrice,
		DailyVelocity:            it.DailyVelocity,
		DaysRemaining:            it.DaysRemaining,
		ProfitPerUnit:            it.ProfitPerUnit,
		PotentialDailyProfitLoss: it.PotentialDailyProfitLoss,
		FrozenValue:              it.FrozenValue,
		GrossSales30d:            it.GrossSales30d,
		NetProfit30d:             it.NetProfit30d,
	}
}

// handleVMI reads the last cached VMI (vendor-managed inventory) analysis —
// no live Billz calls, since that full-catalog computation runs periodically
// in the scheduler. Returns three ranked lists (dead stock / at-risk of
// stockout / top profit sellers) plus full-catalog summary totals.
func (s *Server) handleVMI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	summary, err := scheduler.VMISummaryReport(ctx, s.db)
	if err != nil {
		s.log.Error("webapp: read VMI summary failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	all, err := scheduler.VMIReport(ctx, s.db, "")
	if err != nil {
		s.log.Error("webapp: read VMI report failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := vmiResponse{
		Summary: vmiSummaryResponse{
			TotalFrozenCapital:            summary.TotalFrozenCapital,
			DeadSKUCount:                  summary.DeadSKUCount,
			TotalPotentialDailyProfitLoss: summary.TotalPotentialDailyProfitLoss,
			AtRiskSKUCount:                summary.AtRiskSKUCount,
		},
	}
	for _, it := range all {
		switch it.Category {
		case "dead":
			resp.DeadStock = append(resp.DeadStock, toVMIItemResponse(it))
		case "low", "out":
			resp.AtRisk = append(resp.AtRisk, toVMIItemResponse(it))
		case "top":
			resp.TopSellers = append(resp.TopSellers, toVMIItemResponse(it))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

const (
	categoryTopN      = 5
	categoryPageLimit = 1000
	categoryMaxPages  = 50
	categoryOther     = "Boshqalar"
)

type categoryShare struct {
	Name    string  `json:"name"`
	Revenue float64 `json:"revenue"`
	Percent float64 `json:"percent"`
}

// handleAnalyticsCategories groups this month's sales by top-level product
// category, keeping the top 5 categories by revenue and folding everything
// else (including uncategorized products) into a single "Boshqalar" bucket —
// a pie chart with a long tail of tiny slices isn't readable on a phone.
func (s *Server) handleAnalyticsCategories(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, []categoryShare{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)
	monthStart, _ := monthToDateRange(now)

	shopIDs, err := s.shopIDs(ctx, now)
	if err != nil {
		s.log.Error("webapp: categories: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, []categoryShare{})
		return
	}

	sales, err := s.fetchAllProductSales(ctx, monthStart.Format("2006-01-02"), now.Format("2006-01-02"), shopIDs)
	if err != nil {
		s.log.Error("webapp: categories: failed to fetch product sales", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	byCategory := map[string]float64{}
	var total float64
	for _, p := range sales {
		name := categoryOther
		if len(p.Categories) > 0 && p.Categories[0].Name != "" {
			name = p.Categories[0].Name
		}
		byCategory[name] += p.GrossSales
		total += p.GrossSales
	}

	type kv struct {
		name string
		rev  float64
	}
	var ranked []kv
	for name, rev := range byCategory {
		if name == categoryOther {
			continue
		}
		ranked = append(ranked, kv{name, rev})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].rev > ranked[j].rev })

	var result []categoryShare
	otherSum := byCategory[categoryOther]
	for i, c := range ranked {
		if i < categoryTopN {
			result = append(result, categoryShare{Name: c.name, Revenue: c.rev})
		} else {
			otherSum += c.rev
		}
	}
	if otherSum > 0 {
		result = append(result, categoryShare{Name: categoryOther, Revenue: otherSum})
	}
	if total > 0 {
		for i := range result {
			result[i].Percent = result[i].Revenue / total * 100
		}
	}

	writeJSON(w, http.StatusOK, result)
}

type paymentShare struct {
	Name    string  `json:"name"`
	Sum     float64 `json:"sum"`
	Percent float64 `json:"percent"`
}

// handleAnalyticsPayments breaks down this month's revenue by payment
// method (CLICK, Payme, cash, etc) — a handful of methods, so unlike
// categories there's no need for a "boshqalar" bucket.
func (s *Server) handleAnalyticsPayments(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, []paymentShare{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)
	monthStart, _ := monthToDateRange(now)

	shopIDs, err := s.shopIDs(ctx, now)
	if err != nil {
		s.log.Error("webapp: payments: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, []paymentShare{})
		return
	}

	totals, err := s.billz.TransactionReportTotals(ctx, monthStart.Format("2006-01-02"), now.Format("2006-01-02"), shopIDs)
	if err != nil {
		s.log.Error("webapp: payments: failed to fetch transaction totals", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var total float64
	for _, p := range totals.Payments {
		total += p.Sum
	}

	result := make([]paymentShare, 0, len(totals.Payments))
	for _, p := range totals.Payments {
		if p.Sum <= 0 {
			continue
		}
		share := paymentShare{Name: strings.TrimSpace(p.PaymentTypeName), Sum: p.Sum}
		if total > 0 {
			share.Percent = p.Sum / total * 100
		}
		result = append(result, share)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sum > result[j].Sum })

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) fetchAllProductSales(ctx context.Context, startDate, endDate string, shopIDs []string) ([]billz.ProductSale, error) {
	var all []billz.ProductSale
	for page := 1; page <= categoryMaxPages; page++ {
		report, err := s.billz.ProductSales(ctx, startDate, endDate, shopIDs, page, categoryPageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, report.Products...)
		if len(report.Products) < categoryPageLimit {
			break
		}
	}
	return all, nil
}

// shopIDs fetches today's shop list — Billz's product/stock/table endpoints
// require an explicit shop_ids list (unlike GeneralReport, which defaults to
// "all shops"), so every analytics handler that needs "all shops" borrows it
// from a cheap GeneralReport call instead of hardcoding shop IDs.
func (s *Server) shopIDs(ctx context.Context, now time.Time) ([]string, error) {
	today := now.Format("2006-01-02")
	report, err := s.billz.GeneralReport(ctx, today, today)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(report.ShopStats))
	for _, st := range report.ShopStats {
		ids = append(ids, st.ShopID)
	}
	return ids, nil
}

const (
	forecastHistoryDays = 60
	forecastPageLimit   = 1000
	forecastMaxPages    = 20
	forecastTrendWindow = 14
)

type forecastResponse struct {
	MonthToDateRevenue float64  `json:"month_to_date_revenue"`
	ProjectedTotal     float64  `json:"projected_total"`
	DaysElapsed        int      `json:"days_elapsed"`
	DaysRemaining      int      `json:"days_remaining"`
	LastYearTotal      float64  `json:"last_year_total,omitempty"`
	ChangePercent      *float64 `json:"change_percent,omitempty"`
}

// handleAnalyticsForecast projects this month's final revenue from what's
// already sold plus an estimate for the remaining days. The estimate for
// each remaining day is that day-of-week's historical average (60-day
// window, so it reflects each weekday's typical pace rather than a flat
// daily average) scaled by a recent trend multiplier (last 14 days vs the
// 14 days before that) — a simple model, but it accounts for the two
// variables that matter most for a retail chain: weekly seasonality and
// whether the business is currently trending up or down.
func (s *Server) handleAnalyticsForecast(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, forecastResponse{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)

	shopIDs, err := s.shopIDs(ctx, now)
	if err != nil {
		s.log.Error("webapp: forecast: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, forecastResponse{})
		return
	}

	histStart := now.AddDate(0, 0, -forecastHistoryDays)
	rows, err := s.fetchAllGeneralReportTable(ctx, histStart.Format("2006-01-02"), now.Format("2006-01-02"), shopIDs, "day")
	if err != nil {
		s.log.Error("webapp: forecast: failed to fetch daily history", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	dailyTotals := map[string]float64{}
	for _, row := range rows {
		d, perr := time.ParseInLocation("2006-01-02 15:04:05", row.Date, s.loc)
		if perr != nil {
			continue
		}
		dailyTotals[d.Format("2006-01-02")] += row.GrossSales
	}

	var weekdaySum [7]float64
	var weekdayCount [7]int
	for dateStr, rev := range dailyTotals {
		d, perr := time.ParseInLocation("2006-01-02", dateStr, s.loc)
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

	resp := forecastResponse{
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
	if lastYearReport, err := s.billz.GeneralReport(ctx, lastYearMonthStart.Format("2006-01-02"), lastYearMonthEnd.Format("2006-01-02")); err != nil {
		s.log.Warn("webapp: forecast: last year report failed", "err", err)
	} else if lastYearReport.GrossSales > 0 {
		resp.LastYearTotal = lastYearReport.GrossSales
		pct := (projected - lastYearReport.GrossSales) / lastYearReport.GrossSales * 100
		resp.ChangePercent = &pct
	}

	writeJSON(w, http.StatusOK, resp)
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

func (s *Server) fetchAllGeneralReportTable(ctx context.Context, startDate, endDate string, shopIDs []string, detalization string) ([]billz.ShopDailyStat, error) {
	var all []billz.ShopDailyStat
	for page := 1; page <= forecastMaxPages; page++ {
		table, err := s.billz.GeneralReportTable(ctx, startDate, endDate, shopIDs, detalization, page, forecastPageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, table.ShopStatsByDate...)
		if len(table.ShopStatsByDate) < forecastPageLimit {
			break
		}
	}
	return all, nil
}

func monthToDateRange(now time.Time) (start, end time.Time) {
	start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return start, now
}

// sameRangeLastYear returns the same month, same day-of-month range, one
// year back — the fair baseline for a seasonal retail business (unlike
// last month, which conflates seasonality with real growth/decline).
func sameRangeLastYear(now time.Time) (start, end time.Time) {
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastYearStart := thisMonthStart.AddDate(-1, 0, 0)
	daysIntoMonth := now.Day() - 1
	lastYearEnd := lastYearStart.AddDate(0, 0, daysIntoMonth)
	return lastYearStart, lastYearEnd
}
