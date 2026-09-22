package api

import (
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	window := parseWindow(r.URL.Query().Get("window"))
	now := time.Now()
	from := now.Add(-window)
	snap, err := s.Metrics.Snapshot(ctx, p.ID, from, now)
	if err != nil {
		serverError(w, err)
		return
	}
	step := int64(60)
	switch {
	case window > 7*24*time.Hour:
		step = 6 * 3600
	case window > 24*time.Hour:
		step = 3600
	case window > time.Hour:
		step = 600
	}

	out := []map[string]any{}
	for name, c := range snap.Custom {
		// One series per machine: the same name from four Redis servers is four readings.
		hosts, err := s.Metrics.CustomByHost(ctx, p.ID, name, from, now, step)
		if err != nil {
			serverError(w, err)
			return
		}
		item := map[string]any{"name": name, "kind": c.Kind, "count": c.Count}
		if len(hosts) > 1 {
			list := make([]map[string]any, 0, len(hosts))
			for _, h := range hosts {
				list = append(list, hostMetric(h))
			}
			item["hosts"] = list
		}
		// A gauge read on several machines has no total and no average: only the machines have.
		if c.Kind == "gauge" && len(hosts) > 1 {
			out = append(out, item)
			continue
		}
		item["sum"], item["min"], item["max"] = r2(c.Sum), r2(c.Min), r2(c.Max)
		if c.Count > 0 {
			item["avg"] = r4(c.Sum / float64(c.Count))
		}
		if c.Kind == "timer" {
			item["p95"] = r4(c.Percentile(0.95))
		}
		if c.Kind == "gauge" {
			item["last"] = r2(c.Last)
		}
		item["series"] = mergedSeries(hosts)
		out = append(out, item)
	}
	sortByName(out)
	writeJSON(w, http.StatusOK, map[string]any{"metrics": out, "step": step})
}

// hostMetric is what one machine reported, with its own chart.
func hostMetric(h metrics.HostMetric) map[string]any {
	item := map[string]any{"host": h.Host, "count": h.Count, "sum": r2(h.Sum), "min": r2(h.Min), "max": r2(h.Max), "last": r2(h.Last)}
	if h.Count > 0 {
		item["avg"] = r4(h.Sum / float64(h.Count))
	}
	if h.Kind == "timer" {
		item["p95"] = r4(h.Percentile(0.95))
	}
	item["series"] = points(h.Series)
	return item
}

// mergedSeries adds the machines up step by step: right for a counter or a timer, which is why
// a gauge of several machines never gets here.
func mergedSeries(hosts []metrics.HostMetric) []map[string]any {
	merged := map[int64]*metrics.MetricPoint{}
	var order []int64
	for _, h := range hosts {
		for _, p := range h.Series {
			cur := merged[p.T]
			if cur == nil {
				cur = &metrics.MetricPoint{T: p.T}
				merged[p.T] = cur
				order = append(order, p.T)
			}
			cur.Count += p.Count
			cur.Sum += p.Sum
			cur.Max = math.Max(cur.Max, p.Max)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	list := make([]metrics.MetricPoint, 0, len(order))
	for _, t := range order {
		list = append(list, *merged[t])
	}
	return points(list)
}

func points(list []metrics.MetricPoint) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		point := map[string]any{"t": p.T, "count": p.Count, "sum": r2(p.Sum), "max": r2(p.Max)}
		if p.Count > 0 {
			point["avg"] = r4(p.Sum / float64(p.Count))
		}
		out = append(out, point)
	}
	return out
}

func r4(v float64) float64 { return math.Round(v*10000) / 10000 }

func sortByName(items []map[string]any) {
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
}

func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Channel string `json:"channel"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.Notifier.SendTest(r.Context(), p, body.Channel); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
