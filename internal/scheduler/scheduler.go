// Package scheduler drives the daily summary job and periodic calendar
// reminder checks, both pinned to an explicit timezone rather than the
// host/container's local time.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/robfig/cron/v3"

	"fahriddin-ai/internal/billz"
	"fahriddin-ai/internal/calendar"
	"fahriddin-ai/internal/summary"
)

// Sender delivers a message to the bot owner.
type Sender interface {
	SendToOwner(text string) error
}

type Scheduler struct {
	cron *cron.Cron
	db   *sql.DB
	loc  *time.Location
	log  *slog.Logger

	sender   Sender
	summary  *summary.Builder
	calendar *calendar.Client
	billz    *billz.Client // optional: nil disables the stockout report job

	summaryHour, summaryMinute int
	reminderLeadMin            int
	reminderIntervalMin        int
}

type Config struct {
	Location            *time.Location
	SummaryHour         int
	SummaryMinute       int
	ReminderLeadMinutes int
	ReminderIntervalMin int
}

func New(db *sql.DB, sender Sender, summaryBuilder *summary.Builder, cal *calendar.Client, billzClient *billz.Client, cfg Config, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:                cron.New(cron.WithLocation(cfg.Location)),
		db:                  db,
		loc:                 cfg.Location,
		log:                 log,
		sender:              sender,
		summary:             summaryBuilder,
		calendar:            cal,
		billz:               billzClient,
		summaryHour:         cfg.SummaryHour,
		summaryMinute:       cfg.SummaryMinute,
		reminderLeadMin:     cfg.ReminderLeadMinutes,
		reminderIntervalMin: cfg.ReminderIntervalMin,
	}
}

// Start registers the cron jobs, runs a startup catch-up check for a missed
// daily summary, and begins the scheduler loop.
func (s *Scheduler) Start(ctx context.Context) error {
	summarySpec := fmt.Sprintf("%d %d * * *", s.summaryMinute, s.summaryHour)
	if _, err := s.cron.AddFunc(summarySpec, func() { s.runDailySummary(ctx) }); err != nil {
		return fmt.Errorf("schedule daily summary: %w", err)
	}

	reminderSpec := fmt.Sprintf("*/%d * * * *", s.reminderIntervalMin)
	if _, err := s.cron.AddFunc(reminderSpec, func() { s.checkReminders(ctx) }); err != nil {
		return fmt.Errorf("schedule reminder check: %w", err)
	}

	if s.billz != nil {
		if _, err := s.cron.AddFunc("0 */6 * * *", func() { s.refreshVMIReport(ctx) }); err != nil {
			return fmt.Errorf("schedule VMI report: %w", err)
		}
		// Run once at startup too (in the background — this paginates through
		// a lot of Billz data and shouldn't block the rest of Start).
		go s.refreshVMIReport(ctx)
	}

	s.cron.Start()
	s.catchUpMissedSummary(ctx)
	return nil
}

func (s *Scheduler) Stop() {
	<-s.cron.Stop().Done()
}

// catchUpMissedSummary sends today's summary immediately if it was supposed
// to have already run (e.g. the process restarted shortly after 08:00) but
// hasn't yet, per summary_log.
func (s *Scheduler) catchUpMissedSummary(ctx context.Context) {
	now := time.Now().In(s.loc)
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), s.summaryHour, s.summaryMinute, 0, 0, s.loc)
	if now.Before(scheduled) {
		return
	}

	sent, err := s.summaryAlreadySentToday(ctx, now)
	if err != nil {
		s.log.Error("scheduler: catch-up check failed", "err", err)
		return
	}
	if sent {
		return
	}

	s.log.Info("scheduler: sending catch-up daily summary after restart")
	s.runDailySummary(ctx)
}

func (s *Scheduler) runDailySummary(ctx context.Context) {
	text := s.summary.Generate(ctx)
	status := "ok"
	if err := s.sender.SendToOwner(text); err != nil {
		s.log.Error("scheduler: failed to send daily summary", "err", err)
		status = "send_failed"
	}
	if err := s.recordSummarySent(ctx, status, text); err != nil {
		s.log.Error("scheduler: failed to record summary run", "err", err)
	}
}

// summaryAlreadySentToday only counts a successful ("ok") send — a prior
// failed attempt (e.g. Telegram unreachable) should still be retried on the
// next restart's catch-up check.
func (s *Scheduler) summaryAlreadySentToday(ctx context.Context, now time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM summary_log WHERE run_date = $1 AND status = 'ok')`,
		now.Format("2006-01-02"),
	).Scan(&exists)
	return exists, err
}

func (s *Scheduler) recordSummarySent(ctx context.Context, status, text string) error {
	now := time.Now().In(s.loc)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO summary_log (run_date, status, text) VALUES ($1, $2, $3)
		 ON CONFLICT (run_date) DO UPDATE SET sent_at = now(), status = EXCLUDED.status, text = EXCLUDED.text`,
		now.Format("2006-01-02"), status, text,
	)
	return err
}

// TodaysSummaryText returns the cached text from the last successful summary
// run today, if any. Used by the Mini App dashboard to avoid an extra Claude
// call on every page load.
func TodaysSummaryText(ctx context.Context, db *sql.DB, loc *time.Location) (text string, ok bool, err error) {
	now := time.Now().In(loc)
	var t sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT text FROM summary_log WHERE run_date = $1 AND status = 'ok'`,
		now.Format("2006-01-02"),
	).Scan(&t)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return t.String, t.Valid && t.String != "", nil
}

// SaveSummaryText upserts today's cached summary text (status "ok"). Used by
// the Mini App when it generates a summary on demand outside the daily cron
// job, so later dashboard loads reuse the same cached text.
func SaveSummaryText(ctx context.Context, db *sql.DB, loc *time.Location, text string) error {
	now := time.Now().In(loc)
	_, err := db.ExecContext(ctx,
		`INSERT INTO summary_log (run_date, status, text) VALUES ($1, 'ok', $2)
		 ON CONFLICT (run_date) DO UPDATE SET sent_at = now(), status = 'ok', text = EXCLUDED.text`,
		now.Format("2006-01-02"), text,
	)
	return err
}

// checkReminders looks for calendar events starting within the reminder lead
// window and notifies the owner once per event.
func (s *Scheduler) checkReminders(ctx context.Context) {
	if s.calendar == nil {
		return
	}

	now := time.Now().In(s.loc)
	windowEnd := now.Add(time.Duration(s.reminderLeadMin) * time.Minute)

	events, err := s.calendar.EventsBetween(ctx, now, windowEnd)
	if err != nil {
		s.log.Warn("scheduler: failed to fetch events for reminders", "err", err)
		return
	}

	for _, e := range events {
		sent, err := s.reminderAlreadySent(ctx, e.ID)
		if err != nil {
			s.log.Error("scheduler: reminder dedup check failed", "err", err)
			continue
		}
		if sent {
			continue
		}

		msg := fmt.Sprintf("⏰ Eslatma: \"%s\" — soat %s da", e.Title, e.Start.In(s.loc).Format("15:04"))
		if err := s.sender.SendToOwner(msg); err != nil {
			s.log.Error("scheduler: failed to send reminder", "err", err)
			continue
		}
		if err := s.markReminderSent(ctx, e); err != nil {
			s.log.Error("scheduler: failed to record reminder", "err", err)
		}
	}
}

func (s *Scheduler) reminderAlreadySent(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM reminder_log WHERE event_id = $1)`,
		eventID,
	).Scan(&exists)
	return exists, err
}

func (s *Scheduler) markReminderSent(ctx context.Context, e calendar.Event) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reminder_log (event_id, event_start) VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING`,
		e.ID, e.Start,
	)
	return err
}

// ---------- VMI (vendor-managed inventory) report (Billz, read-only) ----------
//
// A full-catalog pass, unlike the old top-300-sellers-only stockout job:
// every SKU with any stock or any recent sale gets classified into one of
// four buckets so the dashboard can answer three separate questions a
// business owner actually asks:
//   - "dead"  — has stock but hasn't sold at all in 30 days: money frozen
//               in shelf-warmers (frozen_value = stock * retail_price).
//   - "out"/"low" — sells reliably but is out or about to run out: shows
//               the profit at risk per day if it's not restocked in time.
//   - "top"   — sells reliably and isn't at risk: the profit leaderboard.

const (
	vmiSalesWindowDays = 30
	vmiLowStockDays    = 7
	vmiPageLimit       = 1000
	vmiMaxPages        = 300 // full-catalog scan needs more headroom than a top-N job
	vmiDisplayTopN     = 50  // rows kept per category for display; summary totals cover everything
)

type vmiRow struct {
	productID                string
	productName              string
	sku                      string
	category                 string // dead, low, out, top
	totalStock               float64
	retailPrice              *float64
	dailyVelocity            *float64
	daysRemaining            *float64
	profitPerUnit            *float64
	potentialDailyProfitLoss *float64
	frozenValue              *float64
	grossSales30d            float64
	netProfit30d             float64
}

// shopStockAgg is one shop's full-catalog stock totals — computed
// alongside the VMI job's stock-report-table pull, no extra Billz calls.
type shopStockAgg struct {
	name  string
	qty   float64
	value float64
}

// refreshVMIReport recomputes the full VMI analysis and caches it in
// Postgres so the Mini App can read it instantly instead of waiting on a
// full-catalog Billz scan on every page load.
func (s *Scheduler) refreshVMIReport(ctx context.Context) {
	if s.billz == nil {
		return
	}
	s.log.Info("scheduler: refreshing VMI report")

	now := time.Now().In(s.loc)
	startDate := now.AddDate(0, 0, -vmiSalesWindowDays).Format("2006-01-02")
	endDate := now.Format("2006-01-02")

	// Billz's product/stock report endpoints require an explicit shop_ids
	// list (unlike GeneralReport, which defaults to "all shops") — reuse
	// GeneralReport's shop breakdown instead of adding a dedicated call.
	todaysReport, err := s.billz.GeneralReport(ctx, endDate, endDate)
	if err != nil {
		s.log.Error("scheduler: VMI report: failed to get shop list", "err", err)
		return
	}
	shopIDs := make([]string, 0, len(todaysReport.ShopStats))
	for _, st := range todaysReport.ShopStats {
		shopIDs = append(shopIDs, st.ShopID)
	}
	if len(shopIDs) == 0 {
		s.log.Warn("scheduler: VMI report: no shops found, skipping")
		return
	}

	sales, err := s.fetchAllProductSales(ctx, startDate, endDate, shopIDs)
	if err != nil {
		s.log.Error("scheduler: VMI report: failed to fetch product sales", "err", err)
		return
	}
	stock, err := s.fetchAllStockLevels(ctx, endDate, shopIDs)
	if err != nil {
		s.log.Error("scheduler: VMI report: failed to fetch stock levels", "err", err)
		return
	}

	// Full-catalog stock totals, per shop and overall — computed from the
	// same stock-report-table pull above, no extra Billz calls needed.
	stockByShop := make(map[string]*shopStockAgg)
	var globalStockQty, globalStockValue float64
	for _, row := range stock {
		agg, ok := stockByShop[row.ShopID]
		if !ok {
			agg = &shopStockAgg{name: row.ShopName}
			stockByShop[row.ShopID] = agg
		}
		value := row.MeasurementValue * row.RetailPrice
		agg.qty += row.MeasurementValue
		agg.value += value
		globalStockQty += row.MeasurementValue
		globalStockValue += value
	}

	type salesInfo struct {
		name, sku           string
		sold, gross, profit float64
	}
	salesByProduct := make(map[string]*salesInfo)
	for _, p := range sales {
		v, ok := salesByProduct[p.ProductID]
		if !ok {
			v = &salesInfo{name: p.ProductName, sku: p.ProductSKU}
			salesByProduct[p.ProductID] = v
		}
		v.sold += p.SoldMeasurementValue
		v.gross += p.GrossSales
		v.profit += p.NetProfit
	}

	type stockInfo struct {
		name, sku string
		qty       float64
		price     float64
	}
	stockByProduct := make(map[string]*stockInfo)
	for _, row := range stock {
		v, ok := stockByProduct[row.ProductID]
		if !ok {
			v = &stockInfo{name: row.ProductName, sku: row.ProductSKU}
			stockByProduct[row.ProductID] = v
		}
		v.qty += row.MeasurementValue
		if row.RetailPrice > 0 {
			v.price = row.RetailPrice
		}
	}

	ids := make(map[string]bool, len(salesByProduct)+len(stockByProduct))
	for id := range salesByProduct {
		ids[id] = true
	}
	for id := range stockByProduct {
		ids[id] = true
	}

	var dead, atRisk, top []vmiRow
	var totalFrozen, totalDailyLoss float64
	var deadCount, atRiskCount int

	for id := range ids {
		st := stockByProduct[id]
		sa := salesByProduct[id]

		var stockQty, retailPrice float64
		var name, sku string
		if st != nil {
			stockQty, retailPrice, name, sku = st.qty, st.price, st.name, st.sku
		}
		var sold, gross, profit float64
		if sa != nil {
			sold, gross, profit = sa.sold, sa.gross, sa.profit
			if name == "" {
				name, sku = sa.name, sa.sku
			}
		}

		switch {
		case stockQty > 0 && sold == 0:
			price := retailPrice
			frozen := stockQty * price
			totalFrozen += frozen
			deadCount++
			dead = append(dead, vmiRow{
				productID: id, productName: name, sku: sku, category: "dead",
				totalStock: stockQty, retailPrice: &price, frozenValue: &frozen,
				grossSales30d: gross, netProfit30d: profit,
			})
		case sold > 0:
			velocity := sold / float64(vmiSalesWindowDays)
			profitPerUnit := profit / sold
			if stockQty <= 0 {
				dailyLoss := velocity * profitPerUnit
				totalDailyLoss += dailyLoss
				atRiskCount++
				zero := 0.0
				atRisk = append(atRisk, vmiRow{
					productID: id, productName: name, sku: sku, category: "out",
					totalStock: 0, dailyVelocity: &velocity, daysRemaining: &zero,
					profitPerUnit: &profitPerUnit, potentialDailyProfitLoss: &dailyLoss,
					grossSales30d: gross, netProfit30d: profit,
				})
				continue
			}
			daysRemaining := stockQty / velocity
			if daysRemaining <= vmiLowStockDays {
				dailyLoss := velocity * profitPerUnit
				totalDailyLoss += dailyLoss
				atRiskCount++
				atRisk = append(atRisk, vmiRow{
					productID: id, productName: name, sku: sku, category: "low",
					totalStock: stockQty, dailyVelocity: &velocity, daysRemaining: &daysRemaining,
					profitPerUnit: &profitPerUnit, potentialDailyProfitLoss: &dailyLoss,
					grossSales30d: gross, netProfit30d: profit,
				})
			} else {
				top = append(top, vmiRow{
					productID: id, productName: name, sku: sku, category: "top",
					totalStock: stockQty, dailyVelocity: &velocity, daysRemaining: &daysRemaining,
					profitPerUnit: &profitPerUnit, grossSales30d: gross, netProfit30d: profit,
				})
			}
		default:
			continue // no stock and no recent sales — irrelevant, likely delisted
		}
	}

	sort.Slice(dead, func(i, j int) bool { return *dead[i].frozenValue > *dead[j].frozenValue })
	if len(dead) > vmiDisplayTopN {
		dead = dead[:vmiDisplayTopN]
	}
	sort.Slice(atRisk, func(i, j int) bool {
		oi, oj := atRisk[i].category == "out", atRisk[j].category == "out"
		if oi != oj {
			return oi
		}
		return *atRisk[i].daysRemaining < *atRisk[j].daysRemaining
	})
	const atRiskDisplayCap = 200
	if len(atRisk) > atRiskDisplayCap {
		atRisk = atRisk[:atRiskDisplayCap]
	}
	sort.Slice(top, func(i, j int) bool { return top[i].netProfit30d > top[j].netProfit30d })
	if len(top) > vmiDisplayTopN {
		top = top[:vmiDisplayTopN]
	}

	display := make([]vmiRow, 0, len(dead)+len(atRisk)+len(top))
	display = append(display, dead...)
	display = append(display, atRisk...)
	display = append(display, top...)

	if err := s.saveVMIReport(ctx, display, totalFrozen, deadCount, totalDailyLoss, atRiskCount, globalStockQty, globalStockValue, stockByShop); err != nil {
		s.log.Error("scheduler: failed to save VMI report", "err", err)
		return
	}
	s.log.Info("scheduler: VMI report refreshed",
		"dead_display", len(dead), "at_risk_display", len(atRisk), "top_display", len(top),
		"dead_sku_count", deadCount, "total_frozen_capital", totalFrozen,
		"at_risk_sku_count", atRiskCount, "total_potential_daily_profit_loss", totalDailyLoss,
		"total_stock_quantity", globalStockQty, "total_stock_value", globalStockValue, "shops", len(stockByShop),
	)
}

func (s *Scheduler) fetchAllProductSales(ctx context.Context, startDate, endDate string, shopIDs []string) ([]billz.ProductSale, error) {
	var all []billz.ProductSale
	for page := 1; page <= vmiMaxPages; page++ {
		report, err := s.billz.ProductSales(ctx, startDate, endDate, shopIDs, page, vmiPageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, report.Products...)
		if len(report.Products) < vmiPageLimit {
			break
		}
	}
	return all, nil
}

func (s *Scheduler) fetchAllStockLevels(ctx context.Context, reportDate string, shopIDs []string) ([]billz.StockRow, error) {
	var all []billz.StockRow
	for page := 1; page <= vmiMaxPages; page++ {
		report, err := s.billz.StockLevels(ctx, reportDate, shopIDs, page, vmiPageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, report.Rows...)
		if len(report.Rows) < vmiPageLimit {
			break
		}
	}
	return all, nil
}

func (s *Scheduler) saveVMIReport(ctx context.Context, rows []vmiRow, totalFrozen float64, deadCount int, totalDailyLoss float64, atRiskCount int, globalStockQty, globalStockValue float64, stockByShop map[string]*shopStockAgg) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `TRUNCATE vmi_report`); err != nil {
		return err
	}
	for _, r := range rows {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO vmi_report (product_id, product_name, sku, category, total_stock, retail_price,
			                         daily_velocity, days_remaining, profit_per_unit, potential_daily_profit_loss,
			                         frozen_value, gross_sales_30d, net_profit_30d)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			r.productID, r.productName, r.sku, r.category, r.totalStock, r.retailPrice,
			r.dailyVelocity, r.daysRemaining, r.profitPerUnit, r.potentialDailyProfitLoss,
			r.frozenValue, r.grossSales30d, r.netProfit30d,
		)
		if err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO vmi_summary (id, total_frozen_capital, dead_sku_count, total_potential_daily_profit_loss, at_risk_sku_count, total_stock_quantity, total_stock_value, computed_at)
		 VALUES (1, $1, $2, $3, $4, $5, $6, now())
		 ON CONFLICT (id) DO UPDATE SET
		   total_frozen_capital = EXCLUDED.total_frozen_capital,
		   dead_sku_count = EXCLUDED.dead_sku_count,
		   total_potential_daily_profit_loss = EXCLUDED.total_potential_daily_profit_loss,
		   at_risk_sku_count = EXCLUDED.at_risk_sku_count,
		   total_stock_quantity = EXCLUDED.total_stock_quantity,
		   total_stock_value = EXCLUDED.total_stock_value,
		   computed_at = EXCLUDED.computed_at`,
		totalFrozen, deadCount, totalDailyLoss, atRiskCount, globalStockQty, globalStockValue,
	)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `TRUNCATE stock_by_shop`); err != nil {
		return err
	}
	for shopID, agg := range stockByShop {
		if shopID == "" {
			continue
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO stock_by_shop (shop_id, shop_name, total_quantity, total_value) VALUES ($1, $2, $3, $4)`,
			shopID, agg.name, agg.qty, agg.value,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// VMIItem is one cached row from the vmi_report table.
type VMIItem struct {
	ProductName              string
	SKU                      string
	Category                 string
	TotalStock               float64
	RetailPrice              *float64
	DailyVelocity            *float64
	DaysRemaining            *float64
	ProfitPerUnit            *float64
	PotentialDailyProfitLoss *float64
	FrozenValue              *float64
	GrossSales30d            float64
	NetProfit30d             float64
}

// VMISummary holds full-catalog totals, computed before the display table
// was trimmed to its top-N per category.
type VMISummary struct {
	TotalFrozenCapital            float64
	DeadSKUCount                  int
	TotalPotentialDailyProfitLoss float64
	AtRiskSKUCount                int
	TotalStockQuantity            float64
	TotalStockValue               float64
}

// StockByShop is one shop's full-catalog stock totals (quantity + value),
// cached from the VMI job's stock-report-table pull.
type StockByShop struct {
	ShopID        string
	ShopName      string
	TotalQuantity float64
	TotalValue    float64
}

// StockByShopReport returns the cached per-shop stock totals, highest
// quantity first.
func StockByShopReport(ctx context.Context, db *sql.DB) ([]StockByShop, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT shop_id, shop_name, total_quantity, total_value FROM stock_by_shop ORDER BY total_quantity DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []StockByShop
	for rows.Next() {
		var it StockByShop
		if err := rows.Scan(&it.ShopID, &it.ShopName, &it.TotalQuantity, &it.TotalValue); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// VMIReport returns the cached display rows for one category ("dead",
// "low", "out", or "top" — pass "" for all), ordered the same way the
// scheduler job ordered them before trimming.
func VMIReport(ctx context.Context, db *sql.DB, category string) ([]VMIItem, error) {
	query := `SELECT product_name, sku, category, total_stock, retail_price, daily_velocity,
	                 days_remaining, profit_per_unit, potential_daily_profit_loss, frozen_value,
	                 gross_sales_30d, net_profit_30d
	          FROM vmi_report`
	args := []any{}
	if category != "" {
		query += ` WHERE category = $1`
		args = append(args, category)
	}
	query += ` ORDER BY
		CASE category WHEN 'out' THEN 0 WHEN 'low' THEN 1 WHEN 'dead' THEN 2 ELSE 3 END,
		frozen_value DESC NULLS LAST,
		days_remaining ASC NULLS LAST,
		net_profit_30d DESC NULLS LAST`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []VMIItem
	for rows.Next() {
		var it VMIItem
		var retailPrice, dailyVelocity, daysRemaining, profitPerUnit, potentialLoss, frozenValue sql.NullFloat64
		if err := rows.Scan(&it.ProductName, &it.SKU, &it.Category, &it.TotalStock, &retailPrice, &dailyVelocity,
			&daysRemaining, &profitPerUnit, &potentialLoss, &frozenValue, &it.GrossSales30d, &it.NetProfit30d); err != nil {
			return nil, err
		}
		if retailPrice.Valid {
			it.RetailPrice = &retailPrice.Float64
		}
		if dailyVelocity.Valid {
			it.DailyVelocity = &dailyVelocity.Float64
		}
		if daysRemaining.Valid {
			it.DaysRemaining = &daysRemaining.Float64
		}
		if profitPerUnit.Valid {
			it.ProfitPerUnit = &profitPerUnit.Float64
		}
		if potentialLoss.Valid {
			it.PotentialDailyProfitLoss = &potentialLoss.Float64
		}
		if frozenValue.Valid {
			it.FrozenValue = &frozenValue.Float64
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// VMISummaryReport returns the full-catalog totals from the last VMI run.
func VMISummaryReport(ctx context.Context, db *sql.DB) (*VMISummary, error) {
	var s VMISummary
	err := db.QueryRowContext(ctx,
		`SELECT total_frozen_capital, dead_sku_count, total_potential_daily_profit_loss, at_risk_sku_count,
		        total_stock_quantity, total_stock_value
		 FROM vmi_summary WHERE id = 1`,
	).Scan(&s.TotalFrozenCapital, &s.DeadSKUCount, &s.TotalPotentialDailyProfitLoss, &s.AtRiskSKUCount,
		&s.TotalStockQuantity, &s.TotalStockValue)
	if err == sql.ErrNoRows {
		return &VMISummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}
