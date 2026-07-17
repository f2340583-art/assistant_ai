package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"fahriddin-ai/internal/scheduler"
	"fahriddin-ai/internal/summary"
)

type storeResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Revenue       float64 `json:"revenue"`
	Transactions  int     `json:"transactions"`
	AverageCheck  float64 `json:"average_check"`
	SharePercent  float64 `json:"share_percent"`
	StockQuantity float64 `json:"stock_quantity"`
	StockValue    float64 `json:"stock_value"`
}

type businessResponse struct {
	TodayRevenue       float64         `json:"today_revenue"`
	TodayNetRevenue    float64         `json:"today_net_revenue"`
	TodayProfit        float64         `json:"today_profit"`
	TodayTransactions  int             `json:"today_transactions"`
	TodayAverageCheck  float64         `json:"today_average_check"`
	TodayProductsSold  int             `json:"today_products_sold"`
	TotalStockQuantity float64         `json:"total_stock_quantity"`
	TotalStockValue    float64         `json:"total_stock_value"`
	Stores             []storeResponse `json:"stores"`
}

type dashboardResponse struct {
	Business  *businessResponse `json:"business,omitempty"`
	Tiles     []summary.Tile    `json:"tiles"`
	Narrative string            `json:"narrative"`
}

// buildBusiness pulls today's revenue report from Billz. Read-only — never
// calls anything but GeneralReport. Returns nil if Billz isn't configured
// or the call fails, so the dashboard degrades gracefully instead of
// breaking. No today-vs-yesterday trend here — a partial day compared
// against a full day is a misleading comparison; all comparisons in this
// app are year-over-year instead (see internal/webapp/analytics.go).
func (s *Server) buildBusiness(ctx context.Context) *businessResponse {
	if s.billz == nil {
		return nil
	}

	now := time.Now().In(s.loc)
	today := now.Format("2006-01-02")

	todayReport, err := s.billz.GeneralReport(ctx, today, today)
	if err != nil {
		s.log.Error("webapp: billz general report failed", "err", err)
		return nil
	}

	// Stock totals come from the cached VMI job (full-catalog stock-report-
	// table pull, refreshed every 6h) rather than a live Billz call — a
	// live full-catalog pull would be far too slow for a dashboard load.
	stockByShop := map[string]scheduler.StockByShop{}
	if rows, err := scheduler.StockByShopReport(ctx, s.db); err != nil {
		s.log.Warn("webapp: read stock-by-shop failed", "err", err)
	} else {
		for _, r := range rows {
			stockByShop[r.ShopID] = r
		}
	}
	vmiSummary, err := scheduler.VMISummaryReport(ctx, s.db)
	if err != nil {
		s.log.Warn("webapp: read VMI summary failed", "err", err)
		vmiSummary = &scheduler.VMISummary{}
	}

	stores := make([]storeResponse, 0, len(todayReport.ShopStats))
	for _, st := range todayReport.ShopStats {
		var share float64
		if todayReport.NetGrossSales > 0 {
			share = st.NetGrossSales / todayReport.NetGrossSales * 100
		}
		stock := stockByShop[st.ShopID]
		stores = append(stores, storeResponse{
			ID:            st.ShopID,
			Name:          st.ShopName,
			Revenue:       st.NetGrossSales,
			Transactions:  st.TransactionsCount,
			AverageCheck:  st.AverageCheque,
			SharePercent:  share,
			StockQuantity: stock.TotalQuantity,
			StockValue:    stock.TotalValue,
		})
	}
	sort.Slice(stores, func(i, j int) bool { return stores[i].Revenue > stores[j].Revenue })

	return &businessResponse{
		// NetGrossSales (returns/discounts already netted out) matches what
		// Billz's own dashboard displays as "Продажи" — GrossSales runs
		// noticeably higher and would make our numbers look wrong next to
		// the source of truth.
		TodayRevenue:       todayReport.NetGrossSales,
		TodayNetRevenue:    todayReport.NetGrossSales,
		TodayProfit:        todayReport.GrossProfit,
		TodayTransactions:  todayReport.TransactionsCount,
		TodayAverageCheck:  todayReport.AverageCheque,
		TodayProductsSold:  todayReport.ProductsSold,
		TotalStockQuantity: vmiSummary.TotalStockQuantity,
		TotalStockValue:    vmiSummary.TotalStockValue,
		Stores:             stores,
	}
}

// handleDashboard always recomputes the (cheap, no-AI) stat tiles and the
// Billz business section, but reuses today's cached narrative if one exists
// — generating and caching it only on the first request of the day.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tiles := s.summary.Tiles(ctx)
	business := s.buildBusiness(ctx)

	narrative, ok, err := scheduler.TodaysSummaryText(ctx, s.db, s.loc)
	if err != nil {
		s.log.Error("webapp: read cached summary failed", "err", err)
	}
	if !ok {
		narrative = s.summary.Generate(ctx)
		if err := scheduler.SaveSummaryText(ctx, s.db, s.loc, narrative); err != nil {
			s.log.Error("webapp: cache summary failed", "err", err)
		}
	}

	writeJSON(w, http.StatusOK, dashboardResponse{Business: business, Tiles: tiles, Narrative: narrative})
}

// handleDashboardRefresh forces fresh tiles, a fresh business section, and a
// fresh narrative, bypassing cache.
func (s *Server) handleDashboardRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tiles := s.summary.Tiles(ctx)
	business := s.buildBusiness(ctx)
	narrative := s.summary.Generate(ctx)
	if err := scheduler.SaveSummaryText(ctx, s.db, s.loc, narrative); err != nil {
		s.log.Error("webapp: cache summary failed", "err", err)
	}

	writeJSON(w, http.StatusOK, dashboardResponse{Business: business, Tiles: tiles, Narrative: narrative})
}

type taskResponse struct {
	ID          int64      `json:"id"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	open, err := s.tasks.ListOpen(r.Context())
	if err != nil {
		s.log.Error("webapp: list tasks failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]taskResponse, 0, len(open))
	for _, t := range open {
		out = append(out, taskResponse{ID: t.ID, Description: t.Description, DueAt: t.DueAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type addTaskRequest struct {
	Description string     `json:"description"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

func (s *Server) handleAddTask(w http.ResponseWriter, r *http.Request) {
	var req addTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Description == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	id, err := s.tasks.Add(r.Context(), req.Description, req.DueAt)
	if err != nil {
		s.log.Error("webapp: add task failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, taskResponse{ID: id, Description: req.Description, DueAt: req.DueAt})
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.tasks.Complete(r.Context(), id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
