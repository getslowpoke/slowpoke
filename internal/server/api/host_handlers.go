package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/integrations"
	"github.com/getslowpoke/slowpoke/internal/server/mute"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/topology"
)

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var name, kind, version, osName, sources string
	var lastSeen int64
	err := s.DB.Q().Row(ctx, "SELECT name, kind, version, os, sources, last_seen_at FROM hosts WHERE id = ? AND project_id = ?", id, p.ID).Scan(&name, &kind, &version, &osName, &sources, &lastSeen)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	window := parseWindow(r.URL.Query().Get("window"))
	step := int64(60)
	switch {
	case window > 7*24*time.Hour:
		step = 6 * 3600
	case window > 24*time.Hour:
		step = 3600
	case window > time.Hour:
		step = 300
	}
	// The step is inlined (an int): PostgreSQL does not match "bucket % $1" in SELECT with "bucket % $3" in GROUP BY.
	bucketExpr := "bucket - (bucket % " + strconv.FormatInt(step, 10) + ")"
	rows, err := s.DB.Q().Query(ctx, `SELECT `+bucketExpr+`, AVG(cpu), MAX(COALESCE(cpu_peak, cpu)), AVG(cpu_iowait), AVG(cpu_steal), AVG(load1), MAX(cpus),
		AVG(mem_used), MAX(mem_total), AVG(swap_used), MAX(swap_total), SUM(swap_out_pages), SUM(oom_kills),
		AVG(disk_read_bps), AVG(disk_write_bps), AVG(net_rx_bps), AVG(net_tx_bps), MAX(disk_used), MAX(disk_total)
		FROM host_minutes WHERE host_id = ? AND bucket >= ? GROUP BY `+bucketExpr+` ORDER BY 1`, id, time.Now().Add(-window).Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	series := []map[string]any{}
	var oomTotal int64
	for rows.Next() {
		var t int64
		var cpu, cpuMax, iowait, steal, load1, memUsed, swapUsed, readBps, writeBps, rx, tx sql.NullFloat64
		var cpus, memTotal, swapTotal, swapOut, oom, diskUsed, diskTotal sql.NullInt64
		if err := rows.Scan(&t, &cpu, &cpuMax, &iowait, &steal, &load1, &cpus, &memUsed, &memTotal, &swapUsed, &swapTotal, &swapOut, &oom, &readBps, &writeBps, &rx, &tx, &diskUsed, &diskTotal); err != nil {
			continue
		}
		pct := func(used float64, total int64) any {
			if total <= 0 {
				return nil
			}
			return r2(used / float64(total) * 100)
		}
		oomTotal += oom.Int64
		series = append(series, map[string]any{
			"t": t, "cpu": r2(cpu.Float64), "cpu_max": r2(cpuMax.Float64), "iowait": r2(iowait.Float64), "steal": r2(steal.Float64),
			"load1": r2(load1.Float64), "cpus": cpus.Int64, "mem_percent": pct(memUsed.Float64, memTotal.Int64), "swap_percent": pct(swapUsed.Float64, swapTotal.Int64),
			"swap_out_pages": swapOut.Int64, "oom_kills": oom.Int64, "disk_read_bps": r2(readBps.Float64), "disk_write_bps": r2(writeBps.Float64),
			"net_rx_bps": r2(rx.Float64), "net_tx_bps": r2(tx.Float64), "disk_percent": pct(float64(diskUsed.Int64), diskTotal.Int64),
		})
	}
	rows.Close()

	var top sql.NullString
	var memTotal sql.NullInt64
	_ = s.DB.Q().Row(ctx, "SELECT top_processes, mem_total FROM host_minutes WHERE host_id = ? ORDER BY bucket DESC LIMIT 1", id).Scan(&top, &memTotal)
	processes := []map[string]any{}
	if top.Valid {
		var list []map[string]any
		_ = json.Unmarshal([]byte(top.String), &list)
		for _, p := range list {
			if rss, ok := p["rss"].(float64); ok && memTotal.Int64 > 0 {
				p["mem_percent"] = r2(rss / float64(memTotal.Int64) * 100)
			}
			processes = append(processes, p)
		}
	}
	var list []string
	_ = json.Unmarshal([]byte(sources), &list)
	checks, err := integrations.ForHost(ctx, s.DB, p.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	muted, err := mute.HostMuted(ctx, s.DB, p.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"checks":       checks,
		"integrations": integrations.Summarize(checks),
		"host":         map[string]any{"id": id, "name": name, "kind": kind, "version": version, "os": osName, "sources": list, "last_seen_at": lastSeen, "online": time.Since(time.Unix(lastSeen, 0)) < 3*time.Minute},
		"muted":        muted,
		"series":       series,
		"step":         step,
		"processes":    processes,
		"oom_kills":    oomTotal,
	})
}

// handleTopologyLabel renames a machine or a service on the map; an empty name resets it.
func (s *Server) handleTopologyLabel(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		NodeID string `json:"node_id"`
		Label  string `json:"label"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := topology.SetLabel(r.Context(), s.DB, p.ID, body.NodeID, body.Label); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTopologyPlace remembers where a node was dragged on the map; no coordinates put it
// back where the automatic layout wants it.
func (s *Server) handleTopologyPlace(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		NodeID string   `json:"node_id"`
		X      *float64 `json:"x"`
		Y      *float64 `json:"y"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := topology.SetPlace(r.Context(), s.DB, p.ID, body.NodeID, body.X, body.Y); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTopologyPlacesReset puts the whole map back to the automatic layout, names untouched.
func (s *Server) handleTopologyPlacesReset(w http.ResponseWriter, r *http.Request, p project.Project) {
	if err := topology.ClearPlaces(r.Context(), s.DB, p.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request, p project.Project) {
	g, err := topology.Build(r.Context(), s.DB, p.ID, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
