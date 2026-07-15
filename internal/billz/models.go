package billz

// ShopStat is one shop's slice of a GeneralReport.
type ShopStat struct {
	ShopID            string  `json:"shop_id"`
	ShopName          string  `json:"shop_name"`
	GrossSales        float64 `json:"gross_sales"`
	NetGrossSales     float64 `json:"net_gross_sales"`
	GrossProfit       float64 `json:"gross_profit"`
	AverageCheque     float64 `json:"average_cheque"`
	TransactionsCount int     `json:"transactions_count"`
	OrdersCount       int     `json:"orders_count"`
	ProductsSold      int     `json:"products_sold"`
	DiscountPercent   float64 `json:"discount_percent"`
}

// GeneralReport is the response of GET /v1/general-report: company-wide
// totals for a date range, plus a per-shop breakdown.
type GeneralReport struct {
	GrossSales        float64    `json:"gross_sales"`
	NetGrossSales     float64    `json:"net_gross_sales"`
	GrossProfit       float64    `json:"gross_profit"`
	AverageCheque     float64    `json:"average_cheque"`
	TransactionsCount int        `json:"transactions_count"`
	OrdersCount       int        `json:"orders_count"`
	ProductsSold      int        `json:"products_sold"`
	DiscountSum       float64    `json:"discount_sum"`
	DiscountPercent   float64    `json:"discount_percent"`
	ReturnsCount      int        `json:"returns_count"`
	ShopStats         []ShopStat `json:"shop_stats"`
}

// ShopDailyStat is one shop's stats for one date bucket (day or month,
// depending on the requested detalization) from GET /v1/general-report-table.
type ShopDailyStat struct {
	Date              string  `json:"date"`
	ShopID            string  `json:"shop_id"`
	ShopName          string  `json:"shop_name"`
	GrossSales        float64 `json:"gross_sales"`
	NetGrossSales     float64 `json:"net_gross_sales"`
	GrossProfit       float64 `json:"gross_profit"`
	AverageCheque     float64 `json:"average_cheque"`
	TransactionsCount int     `json:"transactions_count"`
	OrdersCount       int     `json:"orders_count"`
	ProductsSold      int     `json:"products_sold"`
}

// GeneralReportTable is the response of GET /v1/general-report-table.
type GeneralReportTable struct {
	ShopStatsByDate []ShopDailyStat `json:"shop_stats_by_date"`
}

// ProductCategory is one category a product belongs to, as embedded directly
// in each GET /v1/product-general-table row.
type ProductCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProductSale is one row of GET /v1/product-general-table: a product's sales
// within one shop for one date bucket (per the requested detalization).
type ProductSale struct {
	ProductID            string            `json:"product_id"`
	ProductName          string            `json:"product_name"`
	ProductSKU           string            `json:"product_sku"`
	ShopID               string            `json:"shop_id"`
	SoldMeasurementValue float64           `json:"sold_measurement_value"`
	GrossSales           float64           `json:"gross_sales"`
	NetProfit            float64           `json:"net_profit"`
	Categories           []ProductCategory `json:"product_categories"`
}

// ProductSalesReport is the response of GET /v1/product-general-table.
type ProductSalesReport struct {
	Count    int           `json:"count"`
	Products []ProductSale `json:"products_stats_by_date"`
}

// StockRow is one (shop, product) row of GET /v1/stock-report-table: the
// stock level of a product in a shop as of the requested report_date.
type StockRow struct {
	ShopID           string  `json:"shop_id"`
	ShopName         string  `json:"shop_name"`
	ProductID        string  `json:"product_id"`
	ProductName      string  `json:"product_name"`
	ProductSKU       string  `json:"product_sku"`
	MeasurementValue float64 `json:"measurement_value"`
	RetailPrice      float64 `json:"retail_price"`
}

// StockReport is the response of GET /v1/stock-report-table.
type StockReport struct {
	Rows  []StockRow `json:"rows"`
	Count int        `json:"count"`
}

// SellerInfo identifies the salesperson attributed to an order item.
type SellerInfo struct {
	Name string `json:"name"`
}

// ItemSeller wraps SellerInfo as it's nested in each order item's "sellers".
type ItemSeller struct {
	Seller SellerInfo `json:"seller"`
}

// OrderItem is one line item of an order, as returned by GET /v3/order-search.
// Only the fields needed for seller-attributed revenue are captured.
type OrderItem struct {
	TotalPrice float64      `json:"total_price"`
	Sellers    []ItemSeller `json:"sellers"`
}

// OrderDetail is the body of one order from GET /v3/order-search.
type OrderDetail struct {
	ShopID    string      `json:"shop_id"`
	CreatedAt string      `json:"created_at"`
	Items     []OrderItem `json:"order_items"`
}

// Order is one entry in an order-search date bucket.
type Order struct {
	Detail OrderDetail `json:"order_detail"`
}

// OrderDateBucket groups orders by calendar date, per the order-search
// response shape.
type OrderDateBucket struct {
	Date   string  `json:"date"`
	Orders []Order `json:"orders"`
}

// OrderSearchReport is the response of GET /v3/order-search.
type OrderSearchReport struct {
	Count        int               `json:"count"`
	OrdersByDate []OrderDateBucket `json:"orders_sorted_by_date_list"`
}

// PaymentTotal is one payment method's total within a TransactionTotals
// report (e.g. CLICK, Payme, Наличные/cash).
type PaymentTotal struct {
	PaymentTypeID   string  `json:"payment_type_id"`
	PaymentTypeName string  `json:"payment_type_name"`
	Sum             float64 `json:"sum"`
}

// TransactionTotals is the response of GET /v1/transaction-report-totals:
// company (or shop-scoped) totals for a period, broken down by payment
// method.
type TransactionTotals struct {
	GrossSales      float64        `json:"gross_sales"`
	TotalPrice      float64        `json:"total_price"`
	DiscountPercent float64        `json:"discount_percent"`
	Payments        []PaymentTotal `json:"payments"`
}
