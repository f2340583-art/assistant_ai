package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fahriddin-ai/internal/reports"
)

const billzNotConfigured = "Billz ulanmagan — bu funksiya hozircha ishlamaydi."

// dispatchTool runs one tool call and returns its tool_result text, an
// optional file attachment (only generate_excel_report produces one), and
// whether the result represents an error (fed back to Claude as
// isError=true, which per the system prompt's ambiguity rule should
// prompt a clarifying question rather than a retry).
func (a *Agent) dispatchTool(ctx context.Context, name string, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	switch name {
	case "get_revenue_report":
		return a.dispatchRevenueReport(ctx, input, now)
	case "compare_periods":
		return a.dispatchComparePeriods(ctx, input)
	case "seller_performance":
		return a.dispatchSellerPerformance(ctx, input, now)
	case "seller_year_over_year":
		return a.dispatchSellerYoY(ctx, input, now)
	case "product_sales_report":
		return a.dispatchProductSales(ctx, input, now)
	case "category_breakdown":
		return a.dispatchCategoryBreakdown(ctx, input, now)
	case "payment_breakdown":
		return a.dispatchPaymentBreakdown(ctx, input, now)
	case "stock_levels":
		return a.dispatchStockLevels(ctx, input, now)
	case "sales_forecast":
		return a.dispatchForecast(ctx, now)
	case "add_task":
		return a.dispatchAddTask(ctx, input)
	case "list_tasks":
		return a.dispatchListTasks(ctx)
	case "complete_task":
		return a.dispatchCompleteTask(ctx, input)
	case "delete_task":
		return a.dispatchDeleteTask(ctx, input)
	case "get_daily_summary":
		return a.summary.Generate(ctx), nil, false
	case "generate_excel_report":
		return a.dispatchGenerateExcel(input)
	default:
		return fmt.Sprintf("Noma'lum vosita: %s", name), nil, true
	}
}

// resolveShopIDs returns []string{shopID} if given, else every shop ID
// (borrowed from a cheap GeneralReport call, since most Billz report
// endpoints require an explicit shop_ids list).
func (a *Agent) resolveShopIDs(ctx context.Context, now time.Time, shopID string) ([]string, error) {
	if shopID != "" {
		return []string{shopID}, nil
	}
	return reports.ShopIDs(ctx, a.billz, now)
}

func fmtMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64) + " so'm"
}

func fmtPct(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*p, 'f', 1, 64) + "%"
}

type revenueReportArgs struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	ShopID    string `json:"shop_id"`
}

func (a *Agent) dispatchRevenueReport(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args revenueReportArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	var shopFilter *string
	if args.ShopID != "" {
		shopFilter = &args.ShopID
	}
	report, err := a.billz.GeneralReport(ctx, args.StartDate, args.EndDate)
	if err != nil {
		a.log.Error("agent: get_revenue_report failed", "err", err)
		return "Hisobotni olishda xatolik yuz berdi.", nil, true
	}
	if shopFilter == nil {
		return fmt.Sprintf("Davr: %s — %s\nSavdo: %s\nTransaksiyalar: %d\nFoyda: %s\nO'rtacha chek: %s",
			args.StartDate, args.EndDate, fmtMoney(report.GrossSales), report.TransactionsCount,
			fmtMoney(report.GrossProfit), fmtMoney(report.AverageCheque)), nil, false
	}
	for _, st := range report.ShopStats {
		if st.ShopID == *shopFilter {
			return fmt.Sprintf("Do'kon: %s\nDavr: %s — %s\nSavdo: %s\nTransaksiyalar: %d\nFoyda: %s",
				st.ShopName, args.StartDate, args.EndDate, fmtMoney(st.GrossSales), st.TransactionsCount, fmtMoney(st.GrossProfit)), nil, false
		}
	}
	return "Bunday do'kon ID topilmadi.", nil, true
}

type comparePeriodsArgs struct {
	PeriodAStart string `json:"period_a_start"`
	PeriodAEnd   string `json:"period_a_end"`
	PeriodBStart string `json:"period_b_start"`
	PeriodBEnd   string `json:"period_b_end"`
	ShopID       string `json:"shop_id"`
}

func (a *Agent) dispatchComparePeriods(ctx context.Context, input json.RawMessage) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args comparePeriodsArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	aStart, err1 := time.Parse("2006-01-02", args.PeriodAStart)
	aEnd, err2 := time.Parse("2006-01-02", args.PeriodAEnd)
	bStart, err3 := time.Parse("2006-01-02", args.PeriodBStart)
	bEnd, err4 := time.Parse("2006-01-02", args.PeriodBEnd)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return "Sanalar YYYY-MM-DD formatida bo'lishi kerak.", nil, true
	}
	var shopFilter *string
	if args.ShopID != "" {
		shopFilter = &args.ShopID
	}
	cmp, err := reports.ComparePeriods(ctx, a.billz, shopFilter, aStart, aEnd, bStart, bEnd)
	if err != nil {
		a.log.Error("agent: compare_periods failed", "err", err)
		return "Solishtirishda xatolik yuz berdi.", nil, true
	}
	return fmt.Sprintf("Davr A (%s — %s): savdo %s, foyda %s, %d transaksiya\nDavr B (%s — %s): savdo %s, foyda %s, %d transaksiya\nO'zgarish: %s",
		args.PeriodAStart, args.PeriodAEnd, fmtMoney(cmp.A.Revenue), fmtMoney(cmp.A.Profit), cmp.A.Transactions,
		args.PeriodBStart, args.PeriodBEnd, fmtMoney(cmp.B.Revenue), fmtMoney(cmp.B.Profit), cmp.B.Transactions,
		fmtPct(cmp.ChangePercent)), nil, false
}

type sellerPerformanceArgs struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	ShopID    string `json:"shop_id"`
	TopN      int    `json:"top_n"`
}

func (a *Agent) dispatchSellerPerformance(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args sellerPerformanceArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	if args.TopN <= 0 {
		args.TopN = 10
	}
	shopIDs, err := a.resolveShopIDs(ctx, now, args.ShopID)
	if err != nil {
		a.log.Error("agent: seller_performance shop list failed", "err", err)
		return "Do'konlar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}
	rows, err := reports.SellerRanking(ctx, a.billz, shopIDs, args.StartDate, args.EndDate, args.TopN)
	if err != nil {
		a.log.Error("agent: seller_performance failed", "err", err)
		return "Sotuvchilar reytingini olishda xatolik yuz berdi.", nil, true
	}
	if len(rows) == 0 {
		return "Bu davrda sotuvlar topilmadi.", nil, false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Sotuvchilar reytingi (%s — %s):\n", args.StartDate, args.EndDate))
	for i, row := range rows {
		sb.WriteString(fmt.Sprintf("%d. %s — %s (%d dona)\n", i+1, row.Name, fmtMoney(row.Revenue), row.ItemsSold))
	}
	return sb.String(), nil, false
}

type sellerYoYArgs struct {
	SellerName  string `json:"seller_name"`
	ShopID      string `json:"shop_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

func (a *Agent) dispatchSellerYoY(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args sellerYoYArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	shopIDs, err := a.resolveShopIDs(ctx, now, args.ShopID)
	if err != nil {
		a.log.Error("agent: seller_year_over_year shop list failed", "err", err)
		return "Do'konlar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}

	var periodStart, periodEnd *time.Time
	if args.PeriodStart != "" && args.PeriodEnd != "" {
		if s, err := time.Parse("2006-01-02", args.PeriodStart); err == nil {
			periodStart = &s
		}
		if e, err := time.Parse("2006-01-02", args.PeriodEnd); err == nil {
			periodEnd = &e
		}
	}

	result, err := reports.SellerYoY(ctx, a.billz, shopIDs, args.SellerName, now, periodStart, periodEnd)
	if err != nil {
		var notFound reports.ErrSellerNotFound
		if errors.As(err, &notFound) {
			return fmt.Sprintf("\"%s\" nomli sotuvchi topilmadi. Mavjud sotuvchilar: %s — aniqlashtirib so'rang.",
				notFound.Query, strings.Join(notFound.Candidates, ", ")), nil, true
		}
		a.log.Error("agent: seller_year_over_year failed", "err", err)
		return "Solishtirishda xatolik yuz berdi.", nil, true
	}

	return fmt.Sprintf("Sotuvchi: %s\n%s: savdo %s, %d dona\n%s: savdo %s, %d dona\nSavdo o'zgarishi: %s\nDona o'zgarishi: %s",
		result.SellerName,
		result.PeriodLabel, fmtMoney(result.ThisPeriod.Revenue), result.ThisPeriod.ItemsSold,
		result.LastYearLabel, fmtMoney(result.LastYearPeriod.Revenue), result.LastYearPeriod.ItemsSold,
		fmtPct(result.RevenueChangePct), fmtPct(result.ItemsChangePct)), nil, false
}

type productSalesArgs struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	ShopID    string `json:"shop_id"`
	TopN      int    `json:"top_n"`
	Order     string `json:"order"`
}

func (a *Agent) dispatchProductSales(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args productSalesArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	if args.TopN <= 0 {
		args.TopN = 10
	}
	shopIDs, err := a.resolveShopIDs(ctx, now, args.ShopID)
	if err != nil {
		a.log.Error("agent: product_sales_report shop list failed", "err", err)
		return "Do'konlar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}
	rows, err := reports.ProductSalesRanking(ctx, a.billz, shopIDs, args.StartDate, args.EndDate, args.TopN, args.Order)
	if err != nil {
		a.log.Error("agent: product_sales_report failed", "err", err)
		return "Tovarlar hisobotini olishda xatolik yuz berdi.", nil, true
	}
	if len(rows) == 0 {
		return "Bu davrda sotuvlar topilmadi.", nil, false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tovarlar (%s — %s):\n", args.StartDate, args.EndDate))
	for i, row := range rows {
		sb.WriteString(fmt.Sprintf("%d. %s — %s (%.0f dona)\n", i+1, row.Name, fmtMoney(row.Revenue), row.Sold))
	}
	return sb.String(), nil, false
}

type periodShopArgs struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	ShopID    string `json:"shop_id"`
}

func (a *Agent) dispatchCategoryBreakdown(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args periodShopArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	shopIDs, err := a.resolveShopIDs(ctx, now, args.ShopID)
	if err != nil {
		a.log.Error("agent: category_breakdown shop list failed", "err", err)
		return "Do'konlar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}
	rows, err := reports.CategoryBreakdown(ctx, a.billz, shopIDs, args.StartDate, args.EndDate)
	if err != nil {
		a.log.Error("agent: category_breakdown failed", "err", err)
		return "Kategoriyalar hisobotini olishda xatolik yuz berdi.", nil, true
	}
	if len(rows) == 0 {
		return "Bu davrda sotuvlar topilmadi.", nil, false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Kategoriyalar bo'yicha (%s — %s):\n", args.StartDate, args.EndDate))
	for _, row := range rows {
		sb.WriteString(fmt.Sprintf("%s — %s (%.1f%%)\n", row.Name, fmtMoney(row.Revenue), row.Percent))
	}
	return sb.String(), nil, false
}

func (a *Agent) dispatchPaymentBreakdown(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args periodShopArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}
	shopIDs, err := a.resolveShopIDs(ctx, now, args.ShopID)
	if err != nil {
		a.log.Error("agent: payment_breakdown shop list failed", "err", err)
		return "Do'konlar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}
	rows, err := reports.PaymentBreakdown(ctx, a.billz, shopIDs, args.StartDate, args.EndDate)
	if err != nil {
		a.log.Error("agent: payment_breakdown failed", "err", err)
		return "To'lov usullari hisobotini olishda xatolik yuz berdi.", nil, true
	}
	if len(rows) == 0 {
		return "Bu davrda to'lovlar topilmadi.", nil, false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("To'lov usullari bo'yicha (%s — %s):\n", args.StartDate, args.EndDate))
	for _, row := range rows {
		sb.WriteString(fmt.Sprintf("%s — %s (%.1f%%)\n", row.Name, fmtMoney(row.Sum), row.Percent))
	}
	return sb.String(), nil, false
}

type stockLevelsArgs struct {
	ShopID       string `json:"shop_id"`
	ProductQuery string `json:"product_query"`
}

func (a *Agent) dispatchStockLevels(ctx context.Context, input json.RawMessage, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	var args stockLevelsArgs
	if err := unmarshalArgs(input, &args); err != nil {
		return "Noto'g'ri parametrlar: " + err.Error(), nil, true
	}

	if args.ShopID != "" {
		rows, err := reports.StockByShop(ctx, a.billz, args.ShopID, args.ProductQuery, now.Format("2006-01-02"))
		if err != nil {
			a.log.Error("agent: stock_levels (shop) failed", "err", err)
			return "Ombor qoldig'ini olishda xatolik yuz berdi.", nil, true
		}
		if len(rows) == 0 {
			return "Hech narsa topilmadi.", nil, false
		}
		var sb strings.Builder
		for i, row := range rows {
			if i >= 30 {
				sb.WriteString(fmt.Sprintf("... yana %d ta tovar\n", len(rows)-30))
				break
			}
			sb.WriteString(fmt.Sprintf("%s (%s) — %.0f dona\n", row.ProductName, row.ProductSKU, row.MeasurementValue))
		}
		return sb.String(), nil, false
	}

	rows, err := reports.StockCompanyWide(ctx, a.db, args.ProductQuery)
	if err != nil {
		a.log.Error("agent: stock_levels (company) failed", "err", err)
		return "Ombor qoldig'ini olishda xatolik yuz berdi.", nil, true
	}
	if len(rows) == 0 {
		return "Hech narsa topilmadi (faqat so'nggi sotilgan tovarlar kuzatiladi).", nil, false
	}
	var sb strings.Builder
	for i, row := range rows {
		if i >= 30 {
			sb.WriteString(fmt.Sprintf("... yana %d ta tovar\n", len(rows)-30))
			break
		}
		sb.WriteString(fmt.Sprintf("%s (%s) — %.0f dona\n", row.ProductName, row.SKU, row.TotalStock))
	}
	return sb.String(), nil, false
}

func (a *Agent) dispatchForecast(ctx context.Context, now time.Time) (string, *Attachment, bool) {
	if a.billz == nil {
		return billzNotConfigured, nil, true
	}
	shopIDs, err := reports.ShopIDs(ctx, a.billz, now)
	if err != nil {
		a.log.Error("agent: sales_forecast shop list failed", "err", err)
		return "Do'konlar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}
	resp, err := reports.Forecast(ctx, a.billz, shopIDs, now, a.loc, a.log)
	if err != nil {
		a.log.Error("agent: sales_forecast failed", "err", err)
		return "Prognozni hisoblashda xatolik yuz berdi.", nil, true
	}
	result := fmt.Sprintf("Oy boshidan hozirgacha: %s\nOy oxiriga prognoz: %s (%d kun o'tdi, %d kun qoldi)",
		fmtMoney(resp.MonthToDateRevenue), fmtMoney(resp.ProjectedTotal), resp.DaysElapsed, resp.DaysRemaining)
	if resp.LastYearTotal > 0 {
		result += fmt.Sprintf("\nO'tgan yil shu oy: %s\nO'zgarish: %s", fmtMoney(resp.LastYearTotal), fmtPct(resp.ChangePercent))
	}
	return result, nil, false
}

type addTaskArgs struct {
	Description string `json:"description"`
	DueAt       string `json:"due_at"`
}

func (a *Agent) dispatchAddTask(ctx context.Context, input json.RawMessage) (string, *Attachment, bool) {
	var args addTaskArgs
	if err := unmarshalArgs(input, &args); err != nil || args.Description == "" {
		return "Vazifa matni ko'rsatilmagan.", nil, true
	}
	var dueAt *time.Time
	if args.DueAt != "" {
		if t, err := time.Parse(time.RFC3339, args.DueAt); err == nil {
			dueAt = &t
		}
	}
	id, err := a.tasks.Add(ctx, args.Description, dueAt)
	if err != nil {
		a.log.Error("agent: add_task failed", "err", err)
		return "Vazifani qo'shishda xatolik yuz berdi.", nil, true
	}
	result := fmt.Sprintf("Vazifa qo'shildi: #%d %s", id, args.Description)
	if dueAt != nil {
		result += " (muddat: " + dueAt.Format("02.01 15:04") + ")"
	}
	return result, nil, false
}

func (a *Agent) dispatchListTasks(ctx context.Context) (string, *Attachment, bool) {
	open, err := a.tasks.ListOpen(ctx)
	if err != nil {
		a.log.Error("agent: list_tasks failed", "err", err)
		return "Vazifalar ro'yxatini olishda xatolik yuz berdi.", nil, true
	}
	if len(open) == 0 {
		return "Ochiq vazifalar yo'q.", nil, false
	}
	var sb strings.Builder
	for _, t := range open {
		line := fmt.Sprintf("#%d %s", t.ID, t.Description)
		if t.DueAt != nil {
			line += " (muddat: " + t.DueAt.Format("02.01 15:04") + ")"
		}
		sb.WriteString(line + "\n")
	}
	return sb.String(), nil, false
}

type completeTaskArgs struct {
	TaskID int64 `json:"task_id"`
}

func (a *Agent) dispatchCompleteTask(ctx context.Context, input json.RawMessage) (string, *Attachment, bool) {
	var args completeTaskArgs
	if err := unmarshalArgs(input, &args); err != nil || args.TaskID == 0 {
		return "Vazifa raqami ko'rsatilmagan.", nil, true
	}
	if err := a.tasks.Complete(ctx, args.TaskID); err != nil {
		return fmt.Sprintf("#%d raqamli vazifa topilmadi yoki allaqachon bajarilgan.", args.TaskID), nil, true
	}
	return fmt.Sprintf("#%d vazifa bajarilgan deb belgilandi.", args.TaskID), nil, false
}

func (a *Agent) dispatchDeleteTask(ctx context.Context, input json.RawMessage) (string, *Attachment, bool) {
	var args completeTaskArgs
	if err := unmarshalArgs(input, &args); err != nil || args.TaskID == 0 {
		return "Vazifa raqami ko'rsatilmagan.", nil, true
	}
	if err := a.tasks.Delete(ctx, args.TaskID); err != nil {
		return fmt.Sprintf("#%d raqamli vazifa topilmadi.", args.TaskID), nil, true
	}
	return fmt.Sprintf("#%d vazifa butunlay o'chirildi.", args.TaskID), nil, false
}

type generateExcelArgs struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

func (a *Agent) dispatchGenerateExcel(input json.RawMessage) (string, *Attachment, bool) {
	var args generateExcelArgs
	if err := unmarshalArgs(input, &args); err != nil || len(args.Columns) == 0 {
		return "Excel uchun ustunlar ko'rsatilmagan.", nil, true
	}
	data, err := reports.ToExcel(args.Title, args.Columns, args.Rows)
	if err != nil {
		a.log.Error("agent: generate_excel_report failed", "err", err)
		return "Excel faylni yaratishda xatolik yuz berdi.", nil, true
	}
	filename := strings.TrimSpace(args.Title)
	if filename == "" {
		filename = "hisobot"
	}
	filename = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, filename) + ".xlsx"

	return fmt.Sprintf("Excel fayl tayyor: %s (%d qator).", filename, len(args.Rows)),
		&Attachment{Filename: filename, Bytes: data}, false
}
