package demo

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// The demo shop: a storefront served by two web machines, a mobile API, a worker for the
// queues and the cron, a primary database and its replica. Thirty days of a story: a new
// checkout, a search rewrite, a regression answered in two days, a partner whose webhooks start
// failing, an import that gets slower every week.

// never is a day that does not come: a fix nobody made, a regression that did not happen.
const never = -1

type site struct {
	source    string
	hosts     []string
	perMinute float64 // requests a minute at the busiest hour
}

var sites = []site{
	{"nginx:shop", []string{"web-1", "web-2"}, 150},
	{"nginx:api", []string{"api-1"}, 80},
}

type use struct {
	query string
	per   float64 // statements per request
}

type call struct {
	host, origin string
	share        float64 // of the request's time spent waiting on it
	fail         float64
	from, to     int // days it happens: [from, to), to never = forever
}

type route struct {
	site         string
	method, path string
	weight       float64
	avg, slow    float64 // average seconds and share of slow requests
	fixedDay     int     // from this day: a quarter of the time, a tenth of the slow ones
	brokenFrom   int     // a regression, for two days
	brokenAvg    float64
	errRate      float64
	uses         []use
	calls        []call
}

func (r route) key() string { return r.method + " " + r.path }

func (r route) sample(day int, rng *rand.Rand) (float64, int) {
	avg, slow, errs := r.avg, r.slow, r.errRate
	if r.fixedDay != never && day >= r.fixedDay {
		avg, slow = avg/4, slow/10
	}
	if r.brokenFrom != never && day >= r.brokenFrom && day < r.brokenFrom+2 {
		avg, slow, errs = r.brokenAvg, 0.2, errs+0.01
	}
	d := avg * (0.5 + rng.ExpFloat64()*0.5)
	if rng.Float64() < slow {
		d = math.Max(d, 2+rng.Float64()*4)
	}
	status := 200
	if rng.Float64() < errs {
		status = 500
	}
	return d, status
}

var routes = []route{
	// Storefront.
	{site: "nginx:shop", method: "GET", path: "/", weight: 16, avg: 0.12, slow: 0.001, fixedDay: never, brokenFrom: never, errRate: 0.000165,
		uses: []use{{"banners", 1}, {"sessions", 1}}},
	{site: "nginx:shop", method: "GET", path: "/products/{id}", weight: 18, avg: 0.09, slow: 0.002, fixedDay: never, brokenFrom: never, errRate: 0.000165,
		uses: []use{{"product", 1}, {"images", 8}, {"sessions", 1}}},
	{site: "nginx:shop", method: "GET", path: "/category/{slug}", weight: 12, avg: 0.25, slow: 0.003, fixedDay: never, brokenFrom: never, errRate: 0.000165,
		uses: []use{{"category", 20}, {"images", 20}, {"sessions", 1}}},
	{site: "nginx:shop", method: "GET", path: "/search", weight: 7, avg: 0.9, slow: 0.12, fixedDay: 9, brokenFrom: never, errRate: 0.000335,
		uses: []use{{"search", 1}, {"sessions", 1}},
		calls: []call{{host: "search.algolia.net", origin: "app/Search/AlgoliaSearch.php:41", share: 0.55, fail: 0.001, from: 9, to: never}}},
	{site: "nginx:shop", method: "GET", path: "/cart", weight: 5, avg: 0.1, slow: 0.002, fixedDay: never, brokenFrom: never, errRate: 0.000165,
		uses: []use{{"cart", 1}, {"sessions", 1}}},
	{site: "nginx:shop", method: "POST", path: "/cart", weight: 5, avg: 0.2, slow: 0.004, fixedDay: never, brokenFrom: never, errRate: 0.000335,
		uses: []use{{"cart", 1}, {"stock", 1}, {"sessions", 1}},
		calls: []call{{host: "rates.shipping.example.com", origin: "app/Services/ShippingRates.php:57", share: 0.35, fail: 0.01, from: 6, to: never}}},
	{site: "nginx:shop", method: "POST", path: "/checkout", weight: 3, avg: 0.8, slow: 0.02, fixedDay: never, brokenFrom: never, errRate: 0.000665,
		uses: []use{{"cart", 1}, {"coupons", 1}, {"stock", 4}, {"audit", 2}, {"sessions", 1}},
		calls: []call{
			{host: "api.stripe.com", origin: "app/Services/Payments/StripeGateway.php:64", share: 0.6, fail: 0.004, from: 3, to: never},
			{host: "api.mailgun.net", origin: "app/Mail/OrderConfirmation.php:22", share: 0.08, fail: 0.002, from: 0, to: 12},
		}},
	{site: "nginx:shop", method: "GET", path: "/account/orders", weight: 4, avg: 0.6, slow: 0.05, fixedDay: 14, brokenFrom: never, errRate: 0.000165,
		uses: []use{{"customer_orders", 1}, {"invoice_count", 10}, {"sessions", 1}}},
	{site: "nginx:shop", method: "GET", path: "/account/invoices/{id}/pdf", weight: 1, avg: 2.4, slow: 0.3, fixedDay: 14, brokenFrom: never, errRate: 0.0005,
		uses: []use{{"invoice", 1}, {"sessions", 1}},
		calls: []call{{host: "s3.eu-west-1.amazonaws.com", origin: "app/Invoices/PdfStore.php:33", share: 0.15, fail: 0.001, from: 0, to: never}}},
	{site: "nginx:shop", method: "POST", path: "/login", weight: 2, avg: 0.35, slow: 0.002, fixedDay: 20, brokenFrom: never, errRate: 0.000165,
		uses: []use{{"customer_email", 1}, {"sessions", 1}}},
	{site: "nginx:shop", method: "GET", path: "/blog/{slug}", weight: 3, avg: 0.08, slow: 0.001, fixedDay: never, brokenFrom: never, errRate: 8.5e-05,
		uses: []use{{"banners", 1}}},
	{site: "nginx:shop", method: "GET", path: "/sitemap.xml", weight: 0.4, avg: 2.5, slow: 0.4, fixedDay: 24, brokenFrom: never, errRate: 0.000335,
		uses: []use{{"sitemap", 1}}},
	{site: "nginx:shop", method: "POST", path: "/newsletter", weight: 0.6, avg: 0.4, slow: 0.01, fixedDay: never, brokenFrom: never, errRate: 0.0005,
		calls: []call{{host: "api.mailgun.net", origin: "app/Http/Controllers/NewsletterController.php:19", share: 0.7, fail: 0.01, from: 0, to: never}}},
	// Long polling for the notification bell: open for 25 seconds on purpose. Left out of the
	// score by the team, which is what the demo shows about exclusions.
	{site: "nginx:shop", method: "GET", path: "/notifications/stream", weight: 1.2, avg: 25, slow: 0, fixedDay: never, brokenFrom: never, errRate: 0.000165},
	{site: "nginx:shop", method: "POST", path: "/webhooks/stripe", weight: 0.8, avg: 0.2, slow: 0.002, fixedDay: never, brokenFrom: never, errRate: 0.000335,
		uses: []use{{"audit", 1}}},
	// Backoffice.
	{site: "nginx:shop", method: "GET", path: "/admin/orders", weight: 1, avg: 1.4, slow: 0.1, fixedDay: 23, brokenFrom: never, errRate: 0.000335,
		uses: []use{{"admin_orders", 1}, {"order_items_count", 1}}},
	{site: "nginx:shop", method: "GET", path: "/admin/reports/sales", weight: 0.08, avg: 6, slow: 0.8, fixedDay: never, brokenFrom: never, errRate: 0.000665,
		uses: []use{{"sales_report", 1}, {"inactive_customers", 1}}},
	{site: "nginx:shop", method: "GET", path: "/admin/export/orders.csv", weight: 0.05, avg: 4, slow: 1, fixedDay: never, brokenFrom: never, errRate: 0.001665,
		uses: []use{{"export_orders", 1}}},
	// Mobile API.
	{site: "nginx:api", method: "GET", path: "/api/v2/products", weight: 30, avg: 0.06, slow: 0.001, fixedDay: never, brokenFrom: never, errRate: 8.5e-05,
		uses: []use{{"category", 1}}},
	{site: "nginx:api", method: "GET", path: "/api/v2/products/{id}", weight: 25, avg: 0.05, slow: 0.001, fixedDay: never, brokenFrom: never, errRate: 8.5e-05,
		uses: []use{{"product", 1}, {"images", 6}}},
	{site: "nginx:api", method: "GET", path: "/api/orders/{id}", weight: 15, avg: 0.15, slow: 0.003, fixedDay: never, brokenFrom: 17, brokenAvg: 1.1, errRate: 0.000165,
		uses: []use{{"orders_by_country", 1}}},
	{site: "nginx:api", method: "POST", path: "/api/v2/orders", weight: 5, avg: 0.6, slow: 0.01, fixedDay: never, brokenFrom: never, errRate: 0.0005,
		uses: []use{{"stock", 3}, {"audit", 2}},
		calls: []call{{host: "api.stripe.com", origin: "app/Services/Payments/StripeGateway.php:64", share: 0.55, fail: 0.004, from: 0, to: never}}},
	{site: "nginx:api", method: "GET", path: "/api/v2/me", weight: 20, avg: 0.03, slow: 0.0005, fixedDay: never, brokenFrom: never, errRate: 8.5e-05,
		uses: []use{{"sessions", 1}}},
	{site: "nginx:api", method: "GET", path: "/api/v2/stores/nearby", weight: 5, avg: 0.45, slow: 0.03, fixedDay: never, brokenFrom: 22, brokenAvg: 0.9, errRate: 0.000165,
		uses: []use{{"stores_nearby", 1}}},
}

type query struct {
	sql, origin, table string
	seconds            float64
	rows               int64
	fixedDay           int
	after              float64 // seconds from fixedDay on
	brokenFrom         int     // two days of regression
	broken             float64
}

func (q query) at(day int) float64 {
	if q.fixedDay != never && day >= q.fixedDay {
		return q.after
	}
	if q.brokenFrom != never && day >= q.brokenFrom && day < q.brokenFrom+2 {
		return q.broken
	}
	return q.seconds
}

var queries = map[string]query{
	"sessions":           {sql: "select * from sessions where id = 'f3a9c1d2' limit 1", origin: "app/Http/Middleware/LoadSession.php:18", table: "sessions", seconds: 0.001, rows: 1, fixedDay: never, brokenFrom: never},
	"banners":            {sql: "select * from banners where active = 1 order by position", origin: "app/Http/Controllers/HomeController.php:24", table: "banners", seconds: 0.004, rows: 12, fixedDay: never, brokenFrom: never},
	"product":            {sql: "select * from products where id = 812 limit 1", origin: "app/Http/Controllers/ProductController.php:27", table: "products", seconds: 0.002, rows: 1, fixedDay: never, brokenFrom: never},
	"images":             {sql: "select * from product_images where product_id = 812", origin: "resources/views/products/card.blade.php:9", table: "product_images", seconds: 0.003, rows: 6, fixedDay: never, brokenFrom: never},
	"category":           {sql: "select * from products where category_id = 12 limit 1", origin: "app/Http/Controllers/CategoryController.php:31", table: "products", seconds: 0.004, rows: 1, fixedDay: never, brokenFrom: never},
	"search":             {sql: "select * from products where name like '%linen%' or description like '%linen%' limit 48", origin: "app/Search/DatabaseSearch.php:52", table: "products", seconds: 1.6, rows: 480000, fixedDay: 9, after: 0.01, brokenFrom: never},
	"cart":               {sql: "select * from carts where session_id = 4815162342 limit 1", origin: "app/Cart/CartRepository.php:33", table: "carts", seconds: 0.35, rows: 900000, fixedDay: never, brokenFrom: never},
	"stock":              {sql: "update stock set quantity = quantity - 1 where product_id = 812", origin: "app/Inventory/Stock.php:61", table: "stock", seconds: 0.006, rows: 1, fixedDay: never, brokenFrom: never},
	"coupons":            {sql: "select * from coupons where code = 'WELCOME10' and (starts_at < now() or starts_at is null)", origin: "app/Checkout/Coupons.php:44", table: "coupons", seconds: 0.08, rows: 42000, fixedDay: never, brokenFrom: never},
	"audit":              {sql: "insert into audit_log (event, subject_id, payload, created_at) values ('order.paid', 991, '{}', now())", origin: "app/Support/Audit.php:15", table: "audit_log", seconds: 0.003, rows: 1, fixedDay: never, brokenFrom: never},
	"customer_orders":    {sql: "select * from orders where note like '%gift%' order by created_at desc limit 50", origin: "app/Http/Controllers/Account/OrdersController.php:38", table: "orders", seconds: 1.8, rows: 480000, fixedDay: 14, after: 0.045, brokenFrom: never},
	"invoice_count":      {sql: "select count(*) as aggregate from invoices where customer_id = '4815' and status = 'paid'", origin: "resources/views/account/orders.blade.php:27", table: "invoices", seconds: 0.035, rows: 2100000, fixedDay: 14, after: 0.001, brokenFrom: never},
	"invoice":            {sql: "select * from invoices where id = 99120 limit 1", origin: "app/Invoices/InvoicePdf.php:29", table: "invoices", seconds: 0.002, rows: 1, fixedDay: never, brokenFrom: never},
	"customer_email":     {sql: "select * from customers where lower(email) = 'ada@example.com'", origin: "app/Auth/CustomerProvider.php:47", table: "customers", seconds: 1.2, rows: 350000, fixedDay: 20, after: 0.002, brokenFrom: never},
	"sitemap":            {sql: "select id, slug, updated_at from products order by updated_at desc", origin: "app/Console/Commands/SitemapGenerate.php:36", table: "products", seconds: 1.9, rows: 480000, fixedDay: 24, after: 0.05, brokenFrom: never},
	"admin_orders":       {sql: "select id, status, total from orders order by created_at desc limit 50 offset 24600", origin: "app/Http/Controllers/Admin/OrderController.php:52", table: "orders", seconds: 0.9, rows: 24650, fixedDay: 23, after: 0.004, brokenFrom: never},
	"order_items_count":  {sql: "select count(*) from order_items", origin: "app/Http/Controllers/Admin/OrderController.php:49", table: "order_items", seconds: 0.4, rows: 6800000, fixedDay: never, brokenFrom: never},
	"sales_report":       {sql: "select date(created_at) as day, sum(total) from orders where created_at > '2026-06-01' group by date(created_at) order by day desc", origin: "app/Reports/SalesReport.php:71", table: "orders", seconds: 4.1, rows: 480000, fixedDay: never, brokenFrom: never},
	"inactive_customers": {sql: "select count(*) from customers where id not in (select customer_id from orders)", origin: "app/Reports/SalesReport.php:88", table: "customers", seconds: 2.2, rows: 350000, fixedDay: never, brokenFrom: never},
	"export_orders":      {sql: "select * from orders o join order_items i on i.order_id = o.id where o.created_at > '2026-01-01'", origin: "app/Exports/OrdersExport.php:24", table: "orders", seconds: 3.2, rows: 6800000, fixedDay: never, brokenFrom: never},
	"orders_by_country":  {sql: "select o.id, o.total from orders o join customers c on c.id = o.customer_id where c.country = 'IT' and o.created_at > '2026-01-01'", origin: "app/Http/Controllers/Api/OrderController.php:66", table: "orders", seconds: 0.09, rows: 90000, fixedDay: never, brokenFrom: 17, broken: 5.4},
	"stores_nearby":      {sql: "select * from stores where round(latitude, 1) = 45.5 and round(longitude, 1) = 9.2", origin: "app/Http/Controllers/Api/StoreController.php:30", table: "stores", seconds: 0.3, rows: 18000, fixedDay: never, brokenFrom: 22, broken: 0.7},
}

type job struct {
	name      string
	perHour   float64
	avg       float64
	fail      float64
	failFrom  int     // from this day a partner is down and more of them fail
	failAfter float64 // the failure rate from then on
	growth    float64 // per day: an import that gets slower every week
	fixedDay  int
}

var jobs = []job{
	{name: "App\\Jobs\\SendOrderConfirmation", perHour: 30, avg: 0.9, fail: 0.002, failFrom: never, fixedDay: never},
	{name: "App\\Jobs\\SendInvoiceEmail", perHour: 14, avg: 0.8, fail: 0.01, failFrom: never, fixedDay: never},
	{name: "App\\Jobs\\GenerateInvoicePdf", perHour: 14, avg: 3.4, fail: 0.004, failFrom: never, fixedDay: 14},
	{name: "App\\Jobs\\SyncStockFromWarehouse", perHour: 12, avg: 14, fail: 0.01, failFrom: never, growth: 0.035, fixedDay: never},
	{name: "App\\Jobs\\IndexProductInSearch", perHour: 40, avg: 0.3, fail: 0.001, failFrom: never, fixedDay: never},
	{name: "App\\Jobs\\ResizeProductImages", perHour: 6, avg: 7.5, fail: 0.02, failFrom: never, fixedDay: never},
	{name: "App\\Jobs\\DispatchPartnerWebhook", perHour: 25, avg: 0.6, fail: 0.004, failFrom: 25, failAfter: 0.14, fixedDay: never},
	{name: "App\\Jobs\\AbandonedCartReminder", perHour: 4, avg: 1.2, fail: 0.01, failFrom: never, fixedDay: never},
}

type cron struct {
	name     string
	every    int     // minutes; 1440 = daily
	at       int     // minute of the day the daily ones start
	seconds  float64 // how long a run takes
	growth   float64 // per day
	exitFail float64
}

var crons = []cron{
	{name: "orders:reconcile", every: 15, seconds: 38},
	{name: "feeds:import-supplier", every: 60, seconds: 150, growth: 0.04},
	{name: "backup:run", every: 1440, at: 2 * 60, seconds: 840},
	{name: "reports:daily", every: 1440, at: 3 * 60, seconds: 540},
	{name: "sitemap:generate", every: 1440, at: 4 * 60, seconds: 190},
	{name: "cache:prune-stale", every: 1440, at: 5*60 + 30, seconds: 25, exitFail: 0.1},
}

type deploy struct{ version, author, message string }

var deploys = map[int]deploy{
	3:  {"v2.4.0", "ada", "New checkout with Stripe"},
	6:  {"v2.4.1", "alan", "Shipping estimate in the cart"},
	9:  {"v2.5.0", "ada", "Search on Algolia, index on orders(created_at)"},
	11: {"v2.5.1", "barbara", "Blog"},
	14: {"v2.6.0", "linus", "Cache invoice counts, faster PDF renderer"},
	17: {"v2.7.0", "grace", "Orders by country report"},
	19: {"v2.7.1", "grace", "Fix orders by country join"},
	20: {"v2.7.2", "barbara", "Index on lower(email) for the login"},
	22: {"v2.8.0", "margaret", "Stores near you"},
	23: {"v2.8.1", "alan", "Keyset pagination in the admin orders"},
	24: {"v2.8.3", "margaret", "Sitemap served from a cache"},
	25: {"v2.8.4", "alan", "Webhooks to the partner warehouse"},
	27: {"v2.9.0", "linus", "Product images resized in the queue"},
}

// explains are what the agent answered to the server's EXPLAIN requests, by table.
var explains = map[string]string{
	"orders":    `{"engine":"mysql","plan":[{"table":"orders","type":"ALL","rows":480000,"Extra":"Using where; Using filesort"}],"tables":{"orders":{"rows":480000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"customer_id","type":"bigint unsigned"},{"name":"note","type":"text"},{"name":"total","type":"decimal(10,2)"},{"name":"status","type":"varchar(20)"},{"name":"created_at","type":"timestamp"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true}]}}}`,
	"invoices":  `{"engine":"mysql","plan":[{"table":"invoices","type":"ALL","rows":2100000,"Extra":"Using where"}],"tables":{"invoices":{"rows":2100000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"customer_id","type":"bigint unsigned"},{"name":"status","type":"varchar(20)"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true}]}}}`,
	"customers": `{"engine":"mysql","plan":[{"table":"customers","type":"ALL","rows":350000,"Extra":"Using where"}],"tables":{"customers":{"rows":350000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"email","type":"varchar(191)"},{"name":"country","type":"char(2)"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true},{"name":"customers_email_unique","columns":["email"],"unique":true}]}}}`,
	"products":  `{"engine":"mysql","plan":[{"table":"products","type":"ALL","rows":480000,"Extra":"Using where"}],"tables":{"products":{"rows":480000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"category_id","type":"bigint unsigned"},{"name":"name","type":"varchar(191)"},{"name":"description","type":"text"},{"name":"slug","type":"varchar(191)"},{"name":"updated_at","type":"timestamp"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true},{"name":"products_category_id_index","columns":["category_id"],"unique":false}]}}}`,
	"carts":     `{"engine":"mysql","plan":[{"table":"carts","type":"ALL","rows":900000,"Extra":"Using where"}],"tables":{"carts":{"rows":900000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"session_id","type":"varchar(64)"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true},{"name":"carts_session_id_index","columns":["session_id"],"unique":false}]}}}`,
	"stores":    `{"engine":"mysql","plan":[{"table":"stores","type":"ALL","rows":18000,"Extra":"Using where"}],"tables":{"stores":{"rows":18000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"latitude","type":"decimal(9,6)"},{"name":"longitude","type":"decimal(9,6)"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true},{"name":"stores_lat_lng_index","columns":["latitude","longitude"],"unique":false}]}}}`,
	"order_items": `{"engine":"mysql","plan":[{"table":"order_items","type":"index","rows":6800000,"Extra":"Using index"}],"tables":{"order_items":{"rows":6800000,"columns":[{"name":"id","type":"bigint unsigned"},{"name":"order_id","type":"bigint unsigned"}],"indexes":[{"name":"PRIMARY","columns":["id"],"unique":true}]}}}`,
}

// totalWeight is the sum of the route weights of each site.
var totalWeight = func() map[string]float64 {
	out := map[string]float64{}
	for _, r := range routes {
		out[r.site] += r.weight
	}
	return out
}()

func pickRoute(source string, rng *rand.Rand) route {
	x := rng.Float64() * totalWeight[source]
	var last route
	for _, r := range routes {
		if r.site != source {
			continue
		}
		last = r
		if x < r.weight {
			return r
		}
		x -= r.weight
	}
	return last
}

// minute is one minute of the shop on every machine: what each agent would send.
// day is the day of the story (0 = the first); a live demo passes a day after every fix.
func minute(t time.Time, loc *time.Location, day, lastDay int, rng *rand.Rand) map[string]*protocol.Payload {
	m := t.Unix() / 60 * 60
	lt := t.In(loc)
	intensity := load(lt)
	out := map[string]*protocol.Payload{}
	for _, mc := range demoMachines() {
		out[mc.name] = &protocol.Payload{Agent: protocol.AgentInfo{Host: mc.name, Kind: "agent", Version: "0.1.0", OS: "linux", Sources: mc.sources},
			Host: []protocol.HostSample{mc.sample(t, rng)}}
	}

	type dbAgg struct {
		n        int64
		sum, max float64
	}
	dbStats := map[string]*dbAgg{}
	http := map[string]*protocol.HTTPBucket{}
	apps := map[string]*protocol.AppBucket{}
	var pdfTimes, searchTimes, paymentTimes []float64
	for _, s := range sites {
		n := int(s.perMinute*intensity) + rng.Intn(8)
		for i := 0; i < n; i++ {
			r := pickRoute(s.source, rng)
			host := s.hosts[rng.Intn(len(s.hosts))]
			d, status := r.sample(day, rng)
			hb := http[host]
			if hb == nil {
				hb = &protocol.HTTPBucket{Source: s.source, Minute: m, Hist: make([]int, len(protocol.HistogramEdges)+1), Endpoints: map[string]protocol.EndpointStat{}}
				http[host] = hb
			}
			addRequest(hb, r.key(), d, status)
			if r.path == "/notifications/stream" {
				continue // long polling: no framework trace, nothing inside worth reading
			}
			app := apps[host]
			if app == nil {
				app = &protocol.AppBucket{Minute: m, Routes: map[string]protocol.RouteStat{}, Queries: map[string]protocol.AppQuery{}}
				apps[host] = app
			}
			rs := app.Routes[r.key()]
			rs.N++
			rs.Sum += d
			rs.Max = math.Max(rs.Max, d)
			if status >= 500 {
				rs.E5++
			}
			for _, u := range r.uses {
				q := queries[u.query]
				k := int64(math.Max(1, math.Round(u.per*(0.8+rng.Float64()*0.4))))
				each := q.at(day) * (0.7 + rng.Float64()*0.6)
				secs := each * float64(k)
				fp := fingerprint.Of(q.sql)
				hash := fingerprint.Hash(fp)
				rs.Queries += int(k)
				rs.DBSum += secs
				if rs.Hashes == nil {
					rs.Hashes = map[string]protocol.RouteQuery{}
				}
				rq := rs.Hashes[hash]
				rq.N += int(k)
				rq.Sum += secs
				rq.Requests++
				rs.Hashes[hash] = rq
				aq := app.Queries[hash]
				aq.Fingerprint, aq.Sample, aq.Origin = fp, fingerprint.Redact(q.sql), q.origin
				aq.N += int(k)
				aq.Sum += secs
				if k >= 5 {
					aq.NPlusOne++
				}
				app.Queries[hash] = aq
				a := dbStats[u.query]
				if a == nil {
					a = &dbAgg{}
					dbStats[u.query] = a
				}
				a.n += k
				a.sum += secs
				a.max = math.Max(a.max, each*1.6)
			}
			waited := 0.0
			for _, c := range r.calls {
				if day < c.from || (c.to != never && day >= c.to) {
					continue
				}
				w := math.Min(d*c.share*(0.8+rng.Float64()*0.2), d*0.95-waited)
				if w <= 0 {
					continue
				}
				waited += w
				if rs.Ext == nil {
					rs.Ext = map[string]protocol.ExtCall{}
				}
				e := rs.Ext[c.host]
				e.N++
				e.Sum += w
				e.Max = math.Max(e.Max, w)
				e.Requests++
				e.Origin = c.origin
				if rng.Float64() < c.fail {
					e.Errors++
				}
				rs.Ext[c.host] = e
				if c.host == "api.stripe.com" {
					paymentTimes = append(paymentTimes, w)
				}
			}
			if waited > 0 {
				rs.ExtSum += waited
				rs.ExtRequests++
			}
			app.Routes[r.key()] = rs
			switch r.path {
			case "/account/invoices/{id}/pdf":
				pdfTimes = append(pdfTimes, d)
			case "/search":
				searchTimes = append(searchTimes, d)
			}
		}
	}
	for host, hb := range http {
		out[host].HTTP = append(out[host].HTTP, *hb)
	}
	for host, app := range apps {
		out[host].App = append(out[host].App, *app)
	}

	// The database sees the statements of every request: a digest of all of them, and the slow
	// log of those over half a second.
	slow := protocol.DBBucket{Source: "mysql-slowlog", Minute: m, Queries: map[string]protocol.QueryStat{}}
	digest := protocol.DBBucket{Source: "mysql-digest", Minute: m, Queries: map[string]protocol.QueryStat{}}
	keys := make([]string, 0, len(dbStats))
	for k := range dbStats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a, q := dbStats[k], queries[k]
		fp := fingerprint.Of(q.sql)
		stat := protocol.QueryStat{Fingerprint: fp, Sample: q.sql, User: "shop", Database: "shop", N: a.n, Sum: a.sum, Max: a.max, Rows: q.rows * a.n}
		digest.Queries[fingerprint.Hash(fp)] = stat
		if a.sum/float64(a.n) >= 0.5 {
			slow.SlowN += int(a.n)
			slow.SlowSum += a.sum
			slow.SlowMax = math.Max(slow.SlowMax, a.max)
			slow.Queries[fingerprint.Hash(fp)] = stat
		}
	}
	db := out["db-1"]
	db.DB = []protocol.DBBucket{slow, digest}
	db.DBStatus = []protocol.DBStatus{{Minute: m, QPS: 80 + 520*intensity, RowsReadPS: 3000 + 120000*intensity, ThreadsRunning: 1 + rng.Intn(5), ThreadsConnected: 30 + rng.Intn(12)}}
	out["db-2"].DBStatus = []protocol.DBStatus{{Minute: m, QPS: 20 + 140*intensity, RowsReadPS: 800 + 40000*intensity, ThreadsRunning: 1 + rng.Intn(2), ThreadsConnected: 8 + rng.Intn(4)}}

	// PHP waiting: the shipping rates API in the afternoon until the cache of day 14, and the
	// sales report building its totals in PHP.
	for _, host := range []string{"web-1", "web-2"} {
		var stalls []protocol.PHPStall
		if day >= 6 && day < 14 && rng.Float64() < 0.1*intensity {
			stalls = append(stalls, protocol.PHPStall{Frame: "curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:44", Code: "app/Services/ShippingRates.php:57", N: 1})
		}
		if rng.Float64() < 0.012*intensity {
			stalls = append(stalls, protocol.PHPStall{Frame: "PDOStatement::fetchAll() vendor/laravel/framework/src/Illuminate/Database/Connection.php:423", Code: "app/Reports/SalesReport.php:71", N: 1})
		}
		if len(stalls) > 0 {
			out[host].PHP = []protocol.PHPBucket{{Source: "php-fpm:shop", Minute: m, N: len(stalls), Stalls: stalls}}
		}
	}

	// Custom metrics, sent by the application through StatsD.
	web := out["web-1"]
	for _, tm := range []protocol.MetricBucket{timer(m, "invoices.pdf", pdfTimes), timer(m, "search.latency", searchTimes), timer(m, "checkout.payment", paymentTimes)} {
		if tm.Count > 0 {
			web.Metrics = append(web.Metrics, tm)
		}
	}
	web.Metrics = append(web.Metrics,
		counter(m, "emails.sent", float64(int(40*intensity)+rng.Intn(6))),
		counter(m, "cart.abandoned", float64(int(6*intensity)+rng.Intn(3))))
	backlog := 20 + 180*intensity*rng.Float64()
	if day >= 25 && day < lastDay {
		backlog += 400 * intensity // webhooks retried while the partner is down
	}
	out["worker-1"].Metrics = []protocol.MetricBucket{gauge(m, "queue.backlog", math.Round(backlog))}

	// Queued jobs and the crontab of the worker.
	w := out["worker-1"]
	for _, j := range jobs {
		n := int(math.Round(j.perHour / 60 * (0.3 + intensity) * (0.6 + rng.Float64()*0.8)))
		if n == 0 {
			continue
		}
		avg := j.avg * math.Pow(1+j.growth, math.Min(float64(day), 30))
		if j.fixedDay != never && day >= j.fixedDay {
			avg /= 6
		}
		fail := j.fail
		if j.failFrom != never && day >= j.failFrom {
			fail = j.failAfter
		}
		failed := 0
		for i := 0; i < n; i++ {
			if rng.Float64() < fail {
				failed++
			}
		}
		w.Jobs = append(w.Jobs, protocol.JobBucket{Minute: m, Kind: "job", Name: j.name, N: n, Failed: failed,
			Sum: avg * float64(n) * (0.8 + rng.Float64()*0.4), Max: avg * (1.5 + rng.Float64())})
	}
	minuteOfDay := lt.Hour()*60 + lt.Minute()
	for _, c := range crons {
		start := (c.every < 1440 && minuteOfDay%c.every == 0) || (c.every == 1440 && minuteOfDay == c.at)
		if !start {
			continue
		}
		secs := c.seconds * math.Pow(1+c.growth, math.Min(float64(day), 30)) * (0.85 + rng.Float64()*0.3)
		exit := 0
		if rng.Float64() < c.exitFail {
			exit = 1
		}
		w.Commands = append(w.Commands, protocol.CommandRun{Host: "worker-1", Name: c.name, StartedAt: m, FinishedAt: m + int64(secs), ExitCode: &exit})
		w.Jobs = append(w.Jobs, protocol.JobBucket{Minute: m, Kind: "command", Name: c.name, N: 1, Failed: exit, Sum: secs, Max: secs})
		// A forgotten crontab on the old server runs the same report on the last day.
		if c.name == "reports:daily" && day == lastDay {
			w.Commands = append(w.Commands, protocol.CommandRun{Host: "old-server", Name: c.name, StartedAt: m + 60, FinishedAt: m + 60 + int64(secs), ExitCode: &exit})
		}
	}
	return out
}

func timer(m int64, name string, values []float64) protocol.MetricBucket {
	b := protocol.MetricBucket{Minute: m, Name: name, Kind: "timer", Hist: make([]int, len(protocol.HistogramEdges)+1)}
	for _, v := range values {
		if b.Count == 0 || v < b.Min {
			b.Min = v
		}
		b.Count++
		b.Sum += v
		b.Max = math.Max(b.Max, v)
		b.Last = v
		b.Hist[protocol.HistogramIndex(v)]++
	}
	return b
}

func counter(m int64, name string, v float64) protocol.MetricBucket {
	return protocol.MetricBucket{Minute: m, Name: name, Kind: "counter", Count: 1, Sum: v, Min: v, Max: v, Last: v}
}

func gauge(m int64, name string, v float64) protocol.MetricBucket {
	return protocol.MetricBucket{Minute: m, Name: name, Kind: "gauge", Count: 1, Sum: v, Min: v, Max: v, Last: v}
}
