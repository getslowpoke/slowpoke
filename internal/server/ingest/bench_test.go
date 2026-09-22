package ingest

import (
	"fmt"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// A busy host: 300 endpoints, 300 queries and 50 custom metrics in one minute.
func busyPayload(batch string, m int64) protocol.Payload {
	hb := protocol.HTTPBucket{Source: "nginx:app", Minute: m, N: 50000, Hist: make([]int, 15), Endpoints: map[string]protocol.EndpointStat{}}
	for i := 0; i < 300; i++ {
		hb.Endpoints[fmt.Sprintf("GET /page-%d/{id}", i)] = protocol.EndpointStat{N: 100, Sum: 12, Max: 1, Slow: 1, SlowSum: 1.1}
	}
	db := protocol.DBBucket{Source: "mysql-digest", Minute: m, Queries: map[string]protocol.QueryStat{}}
	for i := 0; i < 300; i++ {
		fp := fmt.Sprintf("select * from table_%d where id = ?", i)
		db.Queries[fingerprint.Hash(fp)] = protocol.QueryStat{Fingerprint: fp, Sample: fmt.Sprintf("select * from table_%d where id = 1", i), N: 40, Sum: 0.5, Max: 0.1}
	}
	var metrics []protocol.MetricBucket
	for i := 0; i < 50; i++ {
		metrics = append(metrics, protocol.MetricBucket{Minute: m, Name: fmt.Sprintf("metric.%d", i), Kind: "timer", Count: 10, Sum: 1, Min: 0.1, Max: 0.2, Hist: make([]int, 15)})
	}
	return protocol.Payload{BatchID: batch, Agent: agentInfo("nginx:app", "mysql:db"), HTTP: []protocol.HTTPBucket{hb}, DB: []protocol.DBBucket{db}, Metrics: metrics}
}

func BenchmarkIngestBusyHost(b *testing.B) {
	svc, p, ctx := setup(b)
	base := time.Now().Add(-time.Hour).Unix() / 60 * 60
	svc.Now = time.Now
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Ingest(ctx, p, busyPayload(fmt.Sprintf("b%d", i), base+int64(i%60)*60)); err != nil {
			b.Fatal(err)
		}
	}
}
