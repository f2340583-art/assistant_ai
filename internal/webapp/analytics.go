package webapp

import (
	"net/http"
	"time"

	"fahriddin-ai/internal/reports"
	"fahriddin-ai/internal/scheduler"
)

// handleAnalyticsMonthly compares revenue "start of this month to today"
// against the same calendar range one year ago — not last month, which for
// a seasonal retail business is an apples-to-oranges comparison (e.g.
// December vs November). Same month, one year back is the fair baseline.
func (s *Server) handleAnalyticsMonthly(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, reports.PeriodComparison{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)

	thisStart, thisEnd := reports.MonthToDateRange(now)
	lastStart, lastEnd := reports.SameRangeLastYear(now)

	cmp, err := reports.ComparePeriods(ctx, s.billz, nil, thisStart, thisEnd, lastStart, lastEnd)
	if err != nil {
		s.log.Error("webapp: billz monthly report failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	cmp.A.Label = thisStart.Format("02.01") + " – " + thisEnd.Format("02.01")
	cmp.B.Label = lastStart.Format("02.01.06") + " – " + lastEnd.Format("02.01.06")

	writeJSON(w, http.StatusOK, cmp)
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

	resp, err := reports.StoreDetail(ctx, s.billz, shopID, now, s.loc, s.log)
	if err != nil {
		s.log.Error("webapp: billz store detail failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

const employeeTopN = 50

// handleEmployees is the company-wide counterpart to handleStoreDetail's
// per-shop seller breakdown — same order-search + seller-aggregation
// pipeline, just given every shop ID instead of one.
func (s *Server) handleEmployees(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, []reports.SellerRow{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)
	monthStart, monthEnd := reports.MonthToDateRange(now)

	shopIDs, err := reports.ShopIDs(ctx, s.billz, now)
	if err != nil {
		s.log.Error("webapp: employees: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, []reports.SellerRow{})
		return
	}

	rows, err := reports.SellerRanking(ctx, s.billz, shopIDs, monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02"), employeeTopN)
	if err != nil {
		s.log.Error("webapp: employees: failed to fetch orders", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rows)
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

// handleAnalyticsCategories groups this month's sales by top-level product
// category, keeping the top 5 categories by revenue and folding everything
// else (including uncategorized products) into a single "Boshqalar" bucket —
// a pie chart with a long tail of tiny slices isn't readable on a phone.
func (s *Server) handleAnalyticsCategories(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, []reports.CategoryShare{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)
	monthStart, _ := reports.MonthToDateRange(now)

	shopIDs, err := reports.ShopIDs(ctx, s.billz, now)
	if err != nil {
		s.log.Error("webapp: categories: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, []reports.CategoryShare{})
		return
	}

	result, err := reports.CategoryBreakdown(ctx, s.billz, shopIDs, monthStart.Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		s.log.Error("webapp: categories: failed to fetch product sales", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleAnalyticsPayments breaks down this month's revenue by payment
// method (CLICK, Payme, cash, etc) — a handful of methods, so unlike
// categories there's no need for a "boshqalar" bucket.
func (s *Server) handleAnalyticsPayments(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, []reports.PaymentShare{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)
	monthStart, _ := reports.MonthToDateRange(now)

	shopIDs, err := reports.ShopIDs(ctx, s.billz, now)
	if err != nil {
		s.log.Error("webapp: payments: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, []reports.PaymentShare{})
		return
	}

	result, err := reports.PaymentBreakdown(ctx, s.billz, shopIDs, monthStart.Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		s.log.Error("webapp: payments: failed to fetch transaction totals", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleAnalyticsForecast projects this month's final revenue from what's
// already sold plus an estimate for the remaining days.
func (s *Server) handleAnalyticsForecast(w http.ResponseWriter, r *http.Request) {
	if s.billz == nil {
		writeJSON(w, http.StatusOK, reports.ForecastResponse{})
		return
	}
	ctx := r.Context()
	now := time.Now().In(s.loc)

	shopIDs, err := reports.ShopIDs(ctx, s.billz, now)
	if err != nil {
		s.log.Error("webapp: forecast: failed to get shop list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(shopIDs) == 0 {
		writeJSON(w, http.StatusOK, reports.ForecastResponse{})
		return
	}

	resp, err := reports.Forecast(ctx, s.billz, shopIDs, now, s.loc, s.log)
	if err != nil {
		s.log.Error("webapp: forecast: failed to fetch daily history", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
