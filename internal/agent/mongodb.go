package agent

import (
	"encoding/json"
	"strings"
	"time"
)

// mongoSlowParser reads the mongod log, which since 4.4 is one JSON object per line, and keeps the
// operations MongoDB itself called slow: how long they took, how much they had to read, and
// whether they had an index to work with.
//
// What is kept is the shape of the operation - database, collection, kind - never the filter: a
// log line holds what somebody searched for, which is the application's business and not ours.
type mongoSlowParser struct {
	src   Source
	agg   *Aggregator
	seen  map[string]bool // namespaces already given a series of their own
}

// The collections given a timer of their own. A database with a collection per tenant would
// otherwise turn into thousands of series; everything slow is counted in mongodb.<name>.slow
// whatever its namespace, so nothing is lost from the totals.
const maxMongoNamespaces = 50

func NewMongoSlowParser(src Source, agg *Aggregator) LineParser {
	if src.Name == "" {
		src.Name = "mongodb"
	}
	return &mongoSlowParser{src: src, agg: agg, seen: map[string]bool{}}
}

func (p *mongoSlowParser) Feed(line string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' || !strings.Contains(line, "Slow query") {
		return // startup chatter, connections, elections: mongod says a lot
	}
	var entry struct {
		Time struct {
			Date string `json:"$date"`
		} `json:"t"`
		Msg  string `json:"msg"`
		Attr struct {
			Type         string  `json:"type"`
			NS           string  `json:"ns"`
			PlanSummary  string  `json:"planSummary"`
			DocsExamined float64 `json:"docsExamined"`
			KeysExamined float64 `json:"keysExamined"`
			Returned     float64 `json:"nreturned"`
			Millis       float64 `json:"durationMillis"`
			Command      map[string]json.RawMessage `json:"command"`
		} `json:"attr"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Msg != "Slow query" {
		return
	}
	when := time.Now()
	if t, err := time.Parse(time.RFC3339, entry.Time.Date); err == nil {
		when = t
	}
	db := databaseOf(entry.Attr.NS)
	if !p.src.schemaAllowed(db) || p.serverOwn(db) {
		return
	}

	name := func(metric string) string { return "mongodb." + p.src.Name + "." + metric }
	p.agg.AddMetric(when, name("slow"), "counter", 1)
	p.agg.AddMetric(when, name("docs_examined"), "counter", entry.Attr.DocsExamined)
	p.agg.AddMetric(when, name("keys_examined"), "counter", entry.Attr.KeysExamined)
	p.agg.AddMetric(when, name("returned"), "counter", entry.Attr.Returned)
	// A whole collection read for one answer: the shape of a missing index, and the one number
	// worth waking up for.
	if strings.HasPrefix(entry.Attr.PlanSummary, "COLLSCAN") {
		p.agg.AddMetric(when, name("collscan"), "counter", 1)
	}

	op := mongoOperation(entry.Attr.Type, entry.Attr.Command)
	ns := metricSafe(collectionOf(entry.Attr.NS, op, entry.Attr.Command))
	if ns == "" || op == "" || op == "command" {
		return // nothing a person would recognise: the totals above already counted it
	}
	series := ns + "." + op
	if !p.seen[series] {
		if len(p.seen) >= maxMongoNamespaces {
			return // the totals above still hold: only the series of its own is dropped
		}
		p.seen[series] = true
	}
	p.agg.AddMetric(when, name("slow."+series), "timer", entry.Attr.Millis/1000)
}

// serverOwn answers for the databases MongoDB and its tools keep for themselves: the shell's
// telemetry in admin.atlascli, the sessions in config, the oplog in local. A slow one of those is
// nobody's debt - unless the configuration asks for that database by name, because then it is what
// somebody chose to watch.
func (p *mongoSlowParser) serverOwn(db string) bool {
	if len(p.src.Schemas) > 0 {
		return false
	}
	switch db {
	case "admin", "config", "local":
		return true
	}
	return false
}

// mongoOperation is what a person would call it: find, update, aggregate, getmore. The type field
// says it for writes; for a command the first key of the command document is the operation.
func mongoOperation(kind string, command map[string]json.RawMessage) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	// "none" is what mongod writes for entries it does not classify: the command says what it was.
	case "", "none", "command":
		for _, known := range []string{"find", "aggregate", "count", "distinct", "findandmodify", "insert", "update", "delete", "getmore"} {
			if _, ok := command[known]; ok {
				return known
			}
		}
		return "command"
	default:
		return metricSafe(kind)
	}
}

// collectionOf is the namespace a person would recognise. A write command is logged in the
// pseudo-collection "$cmd" of its database, and the collection it touches is the value of the
// operation inside the command document: "shop.$cmd" with {"update":"orders"} is shop.orders.
func collectionOf(ns, op string, command map[string]json.RawMessage) string {
	db, collection, ok := strings.Cut(ns, ".")
	if !ok || collection != "$cmd" {
		return ns
	}
	var target string
	if err := json.Unmarshal(command[op], &target); err == nil && target != "" {
		return db + "." + target
	}
	return ns
}

// databaseOf is the part before the dot of a namespace: "shop.orders" lives in "shop".
func databaseOf(ns string) string {
	if db, _, ok := strings.Cut(ns, "."); ok {
		return db
	}
	return ns
}
