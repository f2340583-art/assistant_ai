package reports

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"fahriddin-ai/internal/billz"
)

// AggregateSellersAll sums each line item's revenue by the seller
// attributed to it (an order item can in principle list more than one
// seller — Billz's own UI treats the first as primary, so we do too).
// Items with no seller recorded are folded into a "Noma'lum" bucket rather
// than silently dropped, so the total still reconciles with the store's
// real revenue. Returns every seller found, sorted by revenue descending —
// use TopSellers to trim.
func AggregateSellersAll(orders []billz.Order) []SellerRow {
	byName := map[string]*SellerRow{}
	var order []string
	for _, o := range orders {
		for _, item := range o.Detail.Items {
			name := SellerUnknown
			if len(item.Sellers) > 0 && item.Sellers[0].Seller.Name != "" {
				name = item.Sellers[0].Seller.Name
			}
			row, ok := byName[name]
			if !ok {
				row = &SellerRow{Name: name}
				byName[name] = row
				order = append(order, name)
			}
			row.Revenue += item.TotalPrice
			row.ItemsSold++
		}
	}

	result := make([]SellerRow, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Revenue > result[j].Revenue })
	return result
}

// TopSellers trims an already-sorted seller list to the top N.
func TopSellers(rows []SellerRow, topN int) []SellerRow {
	if len(rows) > topN {
		return rows[:topN]
	}
	return rows
}

// SellerRanking returns the top sellers (by revenue) across the given shops
// for an arbitrary date range.
func SellerRanking(ctx context.Context, bz *billz.Client, shopIDs []string, startDate, endDate string, topN int) ([]SellerRow, error) {
	orders, err := FetchAllOrders(ctx, bz, startDate, endDate, shopIDs)
	if err != nil {
		return nil, err
	}
	return TopSellers(AggregateSellersAll(orders), topN), nil
}

// SellerYoYResult compares one seller's sales in a period against the same
// calendar range one year back.
type SellerYoYResult struct {
	SellerName       string
	ShopFilter       string // shop ID if scoped, "" if company-wide
	ThisPeriod       SellerRow
	LastYearPeriod   SellerRow // zero-value row if the seller had no sales that period (e.g. new hire)
	RevenueChangePct *float64
	ItemsChangePct   *float64
	PeriodLabel      string
	LastYearLabel    string
}

// ErrSellerNotFound means the requested seller name didn't match any known
// seller in the period being queried. Candidates lists real seller names
// from that period so the caller (the agent) can ask the user to clarify
// instead of failing outright.
type ErrSellerNotFound struct {
	Query      string
	Candidates []string
}

func (e ErrSellerNotFound) Error() string {
	return fmt.Sprintf("seller %q not found", e.Query)
}

const maxSellerCandidates = 15

// SellerYoY composes FetchAllOrders + AggregateSellersAll for a period and
// the same calendar range one year back, then resolves sellerQuery against
// both periods' seller lists (exact match, then substring match). If
// periodStart/periodEnd are nil, defaults to month-to-date.
func SellerYoY(ctx context.Context, bz *billz.Client, shopIDs []string, sellerQuery string, now time.Time, periodStart, periodEnd *time.Time) (SellerYoYResult, error) {
	var start, end time.Time
	if periodStart != nil && periodEnd != nil {
		start, end = *periodStart, *periodEnd
	} else {
		start, end = MonthToDateRange(now)
	}
	lastStart, lastEnd := OneYearBack(start, end)

	var (
		wg                         sync.WaitGroup
		thisOrders, lastYearOrders []billz.Order
		thisErr, lastYearErr       error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		thisOrders, thisErr = FetchAllOrders(ctx, bz, start.Format("2006-01-02"), end.Format("2006-01-02"), shopIDs)
	}()
	go func() {
		defer wg.Done()
		lastYearOrders, lastYearErr = FetchAllOrders(ctx, bz, lastStart.Format("2006-01-02"), lastEnd.Format("2006-01-02"), shopIDs)
	}()
	wg.Wait()
	if thisErr != nil {
		return SellerYoYResult{}, thisErr
	}
	if lastYearErr != nil {
		return SellerYoYResult{}, lastYearErr
	}

	thisRows := AggregateSellersAll(thisOrders)
	lastYearRows := AggregateSellersAll(lastYearOrders)

	thisRow, found := findSeller(thisRows, sellerQuery)
	if !found {
		// Not present this period — check last year too before giving up,
		// so a seller who left still resolves (as a zero row this period)
		// rather than being reported as "not found".
		if lastRow, foundLastYear := findSeller(lastYearRows, sellerQuery); foundLastYear {
			thisRow = SellerRow{Name: lastRow.Name}
			found = true
		}
	}
	if !found {
		return SellerYoYResult{}, ErrSellerNotFound{Query: sellerQuery, Candidates: namesOf(thisRows, maxSellerCandidates)}
	}

	lastYearRow, _ := findSeller(lastYearRows, thisRow.Name)
	lastYearRow.Name = thisRow.Name

	result := SellerYoYResult{
		SellerName:     thisRow.Name,
		ThisPeriod:     thisRow,
		LastYearPeriod: lastYearRow,
		PeriodLabel:    start.Format("02.01") + " – " + end.Format("02.01"),
		LastYearLabel:  lastStart.Format("02.01.06") + " – " + lastEnd.Format("02.01.06"),
	}
	if len(shopIDs) == 1 {
		result.ShopFilter = shopIDs[0]
	}
	if lastYearRow.Revenue > 0 {
		pct := (thisRow.Revenue - lastYearRow.Revenue) / lastYearRow.Revenue * 100
		result.RevenueChangePct = &pct
	}
	if lastYearRow.ItemsSold > 0 {
		pct := float64(thisRow.ItemsSold-lastYearRow.ItemsSold) / float64(lastYearRow.ItemsSold) * 100
		result.ItemsChangePct = &pct
	}
	return result, nil
}

// findSeller resolves a user-typed name against known seller rows: exact
// case-insensitive match first, then substring match either direction.
func findSeller(rows []SellerRow, query string) (SellerRow, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return SellerRow{}, false
	}
	for _, row := range rows {
		if strings.ToLower(row.Name) == q {
			return row, true
		}
	}
	for _, row := range rows {
		name := strings.ToLower(row.Name)
		if strings.Contains(name, q) || strings.Contains(q, name) {
			return row, true
		}
	}
	return SellerRow{}, false
}

func namesOf(rows []SellerRow, limit int) []string {
	names := make([]string, 0, limit)
	for _, row := range rows {
		if len(names) >= limit {
			break
		}
		if row.Name == SellerUnknown {
			continue
		}
		names = append(names, row.Name)
	}
	return names
}
