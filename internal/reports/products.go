package reports

import (
	"context"
	"sort"

	"fahriddin-ai/internal/billz"
)

const (
	CategoryTopN  = 5
	CategoryOther = "Boshqalar"
)

// ProductSalesRanking returns the top (or bottom) N products by revenue for
// the given shops and date range. order is "top" (highest revenue first,
// the default for any unrecognized value) or "bottom" (lowest revenue
// first, e.g. for spotting slow movers among products that did sell at
// all — unlike the VMI dead-stock report, which is about products that
// didn't sell).
func ProductSalesRanking(ctx context.Context, bz *billz.Client, shopIDs []string, startDate, endDate string, topN int, order string) ([]ProductRow, error) {
	sales, err := FetchAllProductSales(ctx, bz, startDate, endDate, shopIDs)
	if err != nil {
		return nil, err
	}

	byProduct := map[string]*ProductRow{}
	var ids []string
	for _, p := range sales {
		row, ok := byProduct[p.ProductID]
		if !ok {
			row = &ProductRow{Name: p.ProductName}
			byProduct[p.ProductID] = row
			ids = append(ids, p.ProductID)
		}
		row.Sold += p.SoldMeasurementValue
		row.Revenue += p.GrossSales
	}

	rows := make([]ProductRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, *byProduct[id])
	}
	if order == "bottom" {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Revenue < rows[j].Revenue })
	} else {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Revenue > rows[j].Revenue })
	}
	if len(rows) > topN {
		rows = rows[:topN]
	}
	return rows, nil
}

// CategoryBreakdown groups sales by top-level product category for the
// given shops and date range, keeping the top 5 categories by revenue and
// folding everything else (including uncategorized products) into a single
// "Boshqalar" bucket — a pie chart with a long tail of tiny slices isn't
// readable on a phone, and a text answer with 30 categories isn't readable
// in a chat either.
func CategoryBreakdown(ctx context.Context, bz *billz.Client, shopIDs []string, startDate, endDate string) ([]CategoryShare, error) {
	sales, err := FetchAllProductSales(ctx, bz, startDate, endDate, shopIDs)
	if err != nil {
		return nil, err
	}

	byCategory := map[string]float64{}
	var total float64
	for _, p := range sales {
		name := CategoryOther
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
		if name == CategoryOther {
			continue
		}
		ranked = append(ranked, kv{name, rev})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].rev > ranked[j].rev })

	var result []CategoryShare
	otherSum := byCategory[CategoryOther]
	for i, c := range ranked {
		if i < CategoryTopN {
			result = append(result, CategoryShare{Name: c.name, Revenue: c.rev})
		} else {
			otherSum += c.rev
		}
	}
	if otherSum > 0 {
		result = append(result, CategoryShare{Name: CategoryOther, Revenue: otherSum})
	}
	if total > 0 {
		for i := range result {
			result[i].Percent = result[i].Revenue / total * 100
		}
	}
	return result, nil
}
