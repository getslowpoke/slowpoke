package agent

import (
	"math"
	"os"
	"strings"
	"testing"
)

// Lines as mongod writes them since 4.4: one JSON object per line, and the slow ones say how long
// they took, what they scanned and whether they had an index to work with.
const mongoSlowLines = `
{"t":{"$date":"2026-09-19T10:00:00.123+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn12","msg":"Slow query","attr":{"type":"command","ns":"shop.orders","command":{"find":"orders","filter":{"status":"paid","email":"ada@example.com"},"$db":"shop"},"planSummary":"COLLSCAN","keysExamined":0,"docsExamined":40000,"nreturned":20,"durationMillis":812}}
{"t":{"$date":"2026-09-19T10:00:01.500+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn12","msg":"Slow query","attr":{"type":"command","ns":"shop.orders","command":{"find":"orders","filter":{"customer_id":7},"$db":"shop"},"planSummary":"IXSCAN { customer_id: 1 }","keysExamined":20,"docsExamined":20,"nreturned":20,"durationMillis":150}}
{"t":{"$date":"2026-09-19T10:00:02.000+02:00"},"s":"I","c":"WRITE","id":51803,"ctx":"conn13","msg":"Slow query","attr":{"type":"update","ns":"shop.invoices","command":{"q":{"_id":1},"u":{"$set":{"sent":true}}},"planSummary":"IDHACK","durationMillis":300}}
{"t":{"$date":"2026-09-19T10:00:03.000+02:00"},"s":"I","c":"NETWORK","id":22943,"ctx":"listener","msg":"Connection accepted","attr":{"remote":"127.0.0.1:55555","connectionId":14}}
{"t":{"$date":"2026-09-19T10:00:04.000+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn15","msg":"Slow query","attr":{"type":"command","ns":"shop.products","command":{"aggregate":"products","pipeline":[{"$match":{"tag":"sale"}}],"$db":"shop"},"planSummary":"COLLSCAN","docsExamined":9000,"nreturned":5,"durationMillis":1200}}
not json at all, as a rotated file can start
{"t":{"$date":"2026-09-19T10:00:05.000+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn16","msg":"Slow query","attr":{"type":"getmore","ns":"shop.orders","durationMillis":90}}
{"t":{"$date":"2026-09-19T10:00:06.000+02:00"},"s":"I","c":"WRITE","id":51803,"ctx":"conn17","msg":"Slow query","attr":{"type":"none","ns":"shop.customers","command":{"find":"customers","filter":{"email":"ada@example.com"}},"planSummary":"COLLSCAN","durationMillis":400}}
{"t":{"$date":"2026-09-19T10:00:07.000+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn18","msg":"Slow query","attr":{"type":"command","ns":"shop.$cmd","command":{"update":"orders","updates":[{"q":{"status":"refunded"},"u":{"$set":{"checked":true}}}]},"durationMillis":500}}
{"t":{"$date":"2026-09-19T10:00:08.000+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn19","msg":"Slow query","attr":{"type":"command","ns":"admin.atlascli","command":{"aggregate":"atlascli"},"durationMillis":700}}
{"t":{"$date":"2026-09-19T10:00:09.000+02:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn20","msg":"Slow query","attr":{"type":"command","ns":"config.transactions","command":{"find":"transactions"},"durationMillis":250}}
`

func feedMongo(t *testing.T, src Source, text string) map[string]float64 {
	t.Helper()
	a := NewAggregator()
	p := NewMongoSlowParser(src, a)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		p.Feed(line)
	}
	return metricsByName(drainAll(a))
}

func TestMongoSlowLogReportsWhatWasSlowAndWhatItScanned(t *testing.T) {
	got := feedMongo(t, Source{Type: "mongodb", Name: "main"}, mongoSlowLines)

	for name, want := range map[string]float64{
		// Timers carry the seconds: two slow finds on shop.orders, 0.812 s and 0.150 s.
		"mongodb.main.slow.shop.orders.find": 0.962,
		"mongodb.main.slow.shop.invoices.update": 0.3,
		"mongodb.main.slow.shop.products.aggregate": 1.2,
		"mongodb.main.slow.shop.orders.getmore": 0.09,
		// Two of the four read a whole collection: that is the number that names a missing index.
		"mongodb.main.collscan": 3,
		"mongodb.main.slow":     7, // find, find, update, aggregate, getmore, and the two below;
		// what mongod and its shell do to themselves, in admin and config, is not the application
		// "type":"none" is what mongod writes for some entries: the command says what it was.
		"mongodb.main.slow.shop.customers.find": 0.4,
		// A write command lives in the namespace "shop.$cmd": the collection is in the command,
		// and it keeps its own series - shop.invoices above is a different collection.
		"mongodb.main.slow.shop.orders.update": 0.5,
		// 40000 + 20 + 9000 documents read to return 45.
		"mongodb.main.docs_examined": 49020,
		"mongodb.main.returned":      45,
	} {
		// Milliseconds turned into seconds do not land on round numbers.
		if v, ok := got[name]; !ok || math.Abs(v-want) > 1e-9 {
			t.Errorf("%s = %v (present %v), want %v", name, v, ok, want)
		}
	}
}

// The log holds what people searched for: an email address, a name, a document. None of it is
// Slowpoke's business - the shape of the operation is.
func TestMongoSlowLogNeverCarriesWhatWasSearchedFor(t *testing.T) {
	a := NewAggregator()
	p := NewMongoSlowParser(Source{Type: "mongodb", Name: "main"}, a)
	for _, line := range strings.Split(strings.TrimSpace(mongoSlowLines), "\n") {
		p.Feed(line)
	}
	drained := drainAll(a)
	for _, m := range drained.Metrics {
		for _, secret := range []string{"ada@example.com", "paid", "customer_id", "sale", "$set"} {
			if strings.Contains(m.Name, secret) {
				t.Fatalf("%q ended up in a metric name: %s", secret, m.Name)
			}
		}
	}
}

func TestMongoSlowLogKeepsItsNamesBoundedAndReadable(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(`{"msg":"Slow query","attr":{"type":"command","ns":"shop.collection-` + string(rune('a'+i%26)) + `-` + strings.Repeat("x", i) + `","command":{"find":"x"},"durationMillis":100}}` + "\n")
	}
	// A namespace with characters a metric name cannot carry.
	b.WriteString(`{"msg":"Slow query","attr":{"type":"command","ns":"shop db.orders 2024","command":{"find":"x"},"durationMillis":100}}` + "\n")
	got := feedMongo(t, Source{Type: "mongodb", Name: "m"}, b.String())

	series := 0
	for name := range got {
		if strings.HasPrefix(name, "mongodb.m.slow.") {
			series++
		}
		if strings.ContainsAny(name, " ") || len(name) > 120 {
			t.Fatalf("a metric name no chart can carry: %q", name)
		}
	}
	if series > maxMongoNamespaces {
		t.Fatalf("%d series, at most %d", series, maxMongoNamespaces)
	}
	if got["mongodb.m.slow"] != 301 {
		t.Errorf("every slow operation is still counted: %v", got["mongodb.m.slow"])
	}
}

// Against a real log: MONGO_LOG=/path/to/mongod.log ./scripts/go test ./internal/agent/ -run
// TestMongoSlowLogAgainstARealLog. The lines above say what the parser does with each field; this
// says the fields are the ones mongod actually writes - including the shapes only a real server
// produces, like the "shop.$cmd" namespace of a write command.
func TestMongoSlowLogAgainstARealLog(t *testing.T) {
	path := os.Getenv("MONGO_LOG")
	if path == "" {
		t.Skip("set MONGO_LOG to a mongod log to run this")
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := feedMongo(t, Source{Type: "mongodb", Name: "lab"}, string(text))

	if got["mongodb.lab.slow"] < 5 {
		t.Fatalf("a log full of slow queries gave %v of them: %v", got["mongodb.lab.slow"], got)
	}
	if got["mongodb.lab.collscan"] < 1 {
		t.Errorf("a collection scanned end to end was not recognised: %v", got)
	}
	if got["mongodb.lab.docs_examined"] <= got["mongodb.lab.returned"] {
		t.Errorf("a slow query reads more than it returns: %v", got)
	}
	var series []string
	for name := range got {
		if strings.HasPrefix(name, "mongodb.lab.slow.") {
			series = append(series, name)
		}
	}
	if len(series) == 0 {
		t.Fatalf("no operation got a timer of its own: %v", got)
	}
	t.Logf("from the log: %d slow, %v collscan, series %v", int(got["mongodb.lab.slow"]), got["mongodb.lab.collscan"], series)
}

// mongosh keeps its own telemetry in admin.atlascli, mongod its sessions in config: neither is the
// application, and a slow one of those is nobody's debt. Found by reading a real log.
func TestMongoSlowLogLeavesTheServersOwnDatabasesAlone(t *testing.T) {
	got := feedMongo(t, Source{Type: "mongodb", Name: "main"}, mongoSlowLines)
	for name := range got {
		for _, own := range []string{".admin.", ".config.", ".local."} {
			if strings.Contains(name, own) {
				t.Errorf("%s is the server's own business, not the application's", name)
			}
		}
	}
	// Unless somebody asks for them by name, because then they are what is being watched.
	watched := feedMongo(t, Source{Type: "mongodb", Name: "main", Schemas: []string{"admin"}}, mongoSlowLines)
	if _, ok := watched["mongodb.main.slow.admin.atlascli.aggregate"]; !ok {
		t.Errorf("a database asked for by name must be read: %v", watched)
	}
}
