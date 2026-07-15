// Package billz is a strictly read-only client for the Billz 2.0 retail API
// (https://api-admin.billz.ai). It must only ever call GET report/reference
// endpoints — never anything that creates, updates, or deletes data in the
// user's live Billz account.
package billz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://api-admin.billz.ai"

// Client authenticates with a secret_token and re-authenticates
// automatically as the access token nears expiry (or on a 401).
type Client struct {
	http        *http.Client
	secretToken string

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

func NewClient(ctx context.Context, secretToken string) (*Client, error) {
	c := &Client{
		http:        &http.Client{Timeout: 15 * time.Second},
		secretToken: secretToken,
	}
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type loginResponse struct {
	Code int `json:"code"`
	Data struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	} `json:"data"`
}

func (c *Client) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"secret_token": c.secretToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("billz login: %w", err)
	}
	defer resp.Body.Close()

	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return fmt.Errorf("billz login: decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || lr.Data.AccessToken == "" {
		return fmt.Errorf("billz login: unexpected status %d", resp.StatusCode)
	}

	c.mu.Lock()
	c.accessToken = lr.Data.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(lr.Data.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return nil
}

func (c *Client) ensureAuth(ctx context.Context) error {
	c.mu.Lock()
	needsLogin := c.accessToken == "" || time.Now().After(c.expiresAt.Add(-1*time.Hour))
	c.mu.Unlock()
	if needsLogin {
		return c.login(ctx)
	}
	return nil
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken
}

// get performs a read-only GET request, retrying once after a fresh login if
// the server responds 401 (expired/invalid token).
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}

	do := func() (*http.Response, error) {
		u := baseURL + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.currentToken())
		return c.http.Do(req)
	}

	resp, err := do()
	if err != nil {
		return nil, fmt.Errorf("billz request: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := c.login(ctx); err != nil {
			return nil, err
		}
		resp, err = do()
		if err != nil {
			return nil, fmt.Errorf("billz request retry: %w", err)
		}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("billz read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("billz request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// GeneralReport returns company-wide revenue/profit stats, with a per-shop
// breakdown, for the given date. Pass the same value for startDate and
// endDate to query a single day. Read-only: GET /v1/general-report.
func (c *Client) GeneralReport(ctx context.Context, startDate, endDate string) (*GeneralReport, error) {
	q := url.Values{}
	q.Set("start_date", startDate)
	if endDate != "" && endDate != startDate {
		q.Set("end_date", endDate)
	}
	q.Set("currency", "UZS")
	q.Set("limit", "50")
	q.Set("page", "1")

	data, err := c.get(ctx, "/v1/general-report", q)
	if err != nil {
		return nil, err
	}

	var report GeneralReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("billz general report: decode: %w", err)
	}
	return &report, nil
}

// GeneralReportTable returns per-shop stats broken down by date bucket
// (detalization: "day", "week", "month", or "year"). shopIDs may be nil/empty
// to include all shops. Paginated: callers spanning many shops/days should
// loop page 1, 2, ... until fewer than limit rows come back. Read-only:
// GET /v1/general-report-table.
func (c *Client) GeneralReportTable(ctx context.Context, startDate, endDate string, shopIDs []string, detalization string, page, limit int) (*GeneralReportTable, error) {
	q := url.Values{}
	q.Set("start_date", startDate)
	if endDate != "" && endDate != startDate {
		q.Set("end_date", endDate)
	}
	q.Set("currency", "UZS")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("page", strconv.Itoa(page))
	if len(shopIDs) > 0 {
		q.Set("shop_ids", strings.Join(shopIDs, ","))
	}
	if detalization != "" {
		q.Set("detalization", detalization)
	}

	data, err := c.get(ctx, "/v1/general-report-table", q)
	if err != nil {
		return nil, err
	}

	var table GeneralReportTable
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("billz general report table: decode: %w", err)
	}
	return &table, nil
}

// ProductSales returns per-product sales rows for the given period and
// shops (shop_ids is required by the Billz API for this endpoint — unlike
// GeneralReport, it does not default to "all shops"). Paginated: callers
// needing the full result set should loop page 1, 2, ... until fewer than
// limit rows come back. Read-only: GET /v1/product-general-table.
func (c *Client) ProductSales(ctx context.Context, startDate, endDate string, shopIDs []string, page, limit int) (*ProductSalesReport, error) {
	q := url.Values{}
	q.Set("start_date", startDate)
	if endDate != "" && endDate != startDate {
		q.Set("end_date", endDate)
	}
	q.Set("currency", "UZS")
	q.Set("shop_ids", strings.Join(shopIDs, ","))
	q.Set("detalization", "month")
	q.Set("page", strconv.Itoa(page))
	q.Set("limit", strconv.Itoa(limit))

	data, err := c.get(ctx, "/v1/product-general-table", q)
	if err != nil {
		return nil, err
	}

	var report ProductSalesReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("billz product sales: decode: %w", err)
	}
	return &report, nil
}

// OrderSearch returns individual orders (with line items and the seller
// attributed to each) for the given period and shops, grouped by date.
// shop_ids is required by the Billz API for this endpoint. Paginated like
// ProductSales — note the response's top-level "count" does not reliably
// reflect the shop_ids filter, so callers should paginate until a page
// returns fewer than limit orders rather than trust count. Read-only:
// GET /v3/order-search.
func (c *Client) OrderSearch(ctx context.Context, startDate, endDate string, shopIDs []string, page, limit int) (*OrderSearchReport, error) {
	q := url.Values{}
	q.Set("start_date", startDate)
	if endDate != "" && endDate != startDate {
		q.Set("end_date", endDate)
	}
	q.Set("shop_ids", strings.Join(shopIDs, ","))
	q.Set("page", strconv.Itoa(page))
	q.Set("limit", strconv.Itoa(limit))

	data, err := c.get(ctx, "/v3/order-search", q)
	if err != nil {
		return nil, err
	}

	var report OrderSearchReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("billz order search: decode: %w", err)
	}
	return &report, nil
}

// TransactionReportTotals returns totals for a period broken down by
// payment method (CLICK, Payme, cash, etc). shop_ids is required by this
// endpoint, same as the other report/table endpoints. Read-only:
// GET /v1/transaction-report-totals.
func (c *Client) TransactionReportTotals(ctx context.Context, startDate, endDate string, shopIDs []string) (*TransactionTotals, error) {
	q := url.Values{}
	q.Set("start_date", startDate)
	if endDate != "" && endDate != startDate {
		q.Set("end_date", endDate)
	}
	q.Set("shop_ids", strings.Join(shopIDs, ","))
	q.Set("currency", "UZS")

	data, err := c.get(ctx, "/v1/transaction-report-totals", q)
	if err != nil {
		return nil, err
	}

	var totals TransactionTotals
	if err := json.Unmarshal(data, &totals); err != nil {
		return nil, fmt.Errorf("billz transaction report totals: decode: %w", err)
	}
	return &totals, nil
}

// StockLevels returns per-(shop, product) stock rows as of reportDate.
// shop_ids is required by the Billz API for this endpoint. Paginated like
// ProductSales. Read-only: GET /v1/stock-report-table.
func (c *Client) StockLevels(ctx context.Context, reportDate string, shopIDs []string, page, limit int) (*StockReport, error) {
	q := url.Values{}
	q.Set("report_date", reportDate)
	q.Set("currency", "UZS")
	q.Set("shop_ids", strings.Join(shopIDs, ","))
	q.Set("page", strconv.Itoa(page))
	q.Set("limit", strconv.Itoa(limit))

	data, err := c.get(ctx, "/v1/stock-report-table", q)
	if err != nil {
		return nil, err
	}

	var report StockReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("billz stock levels: decode: %w", err)
	}
	return &report, nil
}
