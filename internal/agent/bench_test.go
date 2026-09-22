package agent

import (
	"fmt"
	"testing"
)

// The agent must stay light: these benchmarks guard the hot paths.
func BenchmarkNginxLine(b *testing.B) {
	a := NewAggregator()
	p := NewNginxParser(Source{Name: "app", SlowSeconds: 1}, a)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf(`10.0.0.%d - - [16/Sep/2026:12:00:%02d +0200] "GET /api/orders/%d?x=1 HTTP/1.1" 200 512 "-" "Mozilla/5.0" rt=0.%03d`, i%250, i%60, i*37, i*7%1000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Feed(lines[i%100])
	}
}

func BenchmarkSlowQuery(b *testing.B) {
	a := NewAggregator()
	q := SlowQuery{Time: t0, User: "app", Seconds: 1.2, SQL: "select * from orders where customer_id = 4815 and status in ('paid', 'sent') order by created_at desc limit 50"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.AddSlowQuery("mysql-slowlog", q)
	}
}
