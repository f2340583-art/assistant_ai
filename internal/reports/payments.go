package reports

import (
	"context"
	"sort"
	"strings"

	"fahriddin-ai/internal/billz"
)

// PaymentBreakdown breaks revenue down by payment method (CLICK, Payme,
// cash, etc) for the given shops and date range — a handful of methods, so
// unlike categories there's no need for an "other" bucket.
func PaymentBreakdown(ctx context.Context, bz *billz.Client, shopIDs []string, startDate, endDate string) ([]PaymentShare, error) {
	totals, err := bz.TransactionReportTotals(ctx, startDate, endDate, shopIDs)
	if err != nil {
		return nil, err
	}

	var total float64
	for _, p := range totals.Payments {
		total += p.Sum
	}

	result := make([]PaymentShare, 0, len(totals.Payments))
	for _, p := range totals.Payments {
		if p.Sum <= 0 {
			continue
		}
		share := PaymentShare{Name: strings.TrimSpace(p.PaymentTypeName), Sum: p.Sum}
		if total > 0 {
			share.Percent = p.Sum / total * 100
		}
		result = append(result, share)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sum > result[j].Sum })
	return result, nil
}
