package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// Check statuses. "off" is not an error: many capabilities are optional and some teams do not
// want them (a database user, the slow log); the UI explains how to turn them on and off.
const (
	StatusOK      = "ok"
	StatusOff     = "off"
	StatusError   = "error"
	StatusWaiting = "waiting"
)

func item(key, status string, optional bool, reason, detail string) protocol.CheckItem {
	return protocol.CheckItem{Key: key, Status: status, Optional: optional, Reason: reason, Detail: truncate(detail, 300)}
}

// mysqlFacts is what the agent could learn about a MySQL or MariaDB server with its own user.
type mysqlFacts struct {
	Connected       bool
	ConnectError    string
	Version         string
	PerfSchema      bool
	DigestsEnabled  bool // digests: true in agent.yaml
	DigestsReadable bool
	DigestsError    string
	HasSamples      bool // performance_schema keeps a statement with its values (MySQL 8)
	SlowLogOn       bool
	SlowLogRead     bool // a mysql-slowlog source reads it
	ExplainEnabled  bool
	GlobalSelect    bool
	SelectOn        map[string]bool
	Schemas         []string
}

func mysqlCheckItems(f mysqlFacts) []protocol.CheckItem {
	if !f.Connected {
		return []protocol.CheckItem{item("connection", StatusError, false, "connect_failed", f.ConnectError)}
	}
	out := []protocol.CheckItem{item("connection", StatusOK, false, "", "")}

	switch {
	case !f.DigestsEnabled:
		out = append(out, item("statistics", StatusOff, true, "disabled", ""))
	case !f.PerfSchema:
		out = append(out, item("statistics", StatusOff, true, "performance_schema_off", ""))
	case f.DigestsError != "":
		out = append(out, item("statistics", StatusError, true, "no_performance_schema_grant", f.DigestsError))
	default:
		out = append(out, item("statistics", StatusOK, true, "", ""))
	}

	slowLog := false
	switch {
	case f.SlowLogOn && f.SlowLogRead:
		slowLog = true
		out = append(out, item("slow_log", StatusOK, true, "", ""))
	case f.SlowLogOn:
		out = append(out, item("slow_log", StatusOff, true, "not_read", ""))
	default:
		out = append(out, item("slow_log", StatusOff, true, "slow_log_off", ""))
	}

	var missing []string
	if !f.GlobalSelect {
		for _, s := range f.Schemas {
			if !f.SelectOn[s] {
				missing = append(missing, s)
			}
		}
		if len(f.Schemas) == 0 && len(f.SelectOn) == 0 {
			missing = append(missing, "*")
		}
	}
	switch {
	case !f.ExplainEnabled:
		out = append(out, item("plans", StatusOff, true, "disabled", ""))
	case len(missing) > 0:
		out = append(out, item("plans", StatusOff, true, "no_select", strings.Join(missing, ", ")))
	case !f.HasSamples && !slowLog:
		out = append(out, item("plans", StatusOff, true, "needs_slow_log", ""))
	default:
		out = append(out, item("plans", StatusOK, true, "", ""))
	}
	return out
}

type postgresFacts struct {
	Connected          bool
	ConnectError       string
	VersionNum         int
	DigestsEnabled     bool
	StatementsLoaded   bool // extension created and preloaded
	StatementsReadable bool
	StatementsError    string
	ReadAllStats       bool
	ExplainEnabled     bool
	CanSelect          bool
}

func postgresCheckItems(f postgresFacts) []protocol.CheckItem {
	if !f.Connected {
		return []protocol.CheckItem{item("connection", StatusError, false, "connect_failed", f.ConnectError)}
	}
	out := []protocol.CheckItem{item("connection", StatusOK, false, "", "")}
	switch {
	case !f.DigestsEnabled:
		out = append(out, item("statistics", StatusOff, true, "disabled", ""))
	case !f.StatementsLoaded:
		out = append(out, item("statistics", StatusOff, true, "pg_stat_statements_missing", ""))
	case f.StatementsError != "" || !f.StatementsReadable:
		out = append(out, item("statistics", StatusError, true, "no_stats_grant", f.StatementsError))
	case !f.ReadAllStats:
		// Readable, but only the agent's own statements: the application's stay hidden.
		out = append(out, item("statistics", StatusOff, true, "no_read_all_stats", ""))
	default:
		out = append(out, item("statistics", StatusOK, true, "", ""))
	}
	switch {
	case !f.ExplainEnabled:
		out = append(out, item("plans", StatusOff, true, "disabled", ""))
	case f.VersionNum > 0 && f.VersionNum < 160000:
		out = append(out, item("plans", StatusOff, true, "needs_postgres_16", ""))
	case !f.CanSelect:
		out = append(out, item("plans", StatusOff, true, "no_select", ""))
	default:
		out = append(out, item("plans", StatusOK, true, "", ""))
	}
	return out
}

// fileFacts describe a log file the agent reads.
type fileFacts struct {
	Readable     bool
	Missing      bool // the file is not there yet: a step of the install, not a fault
	ReadError    string
	Lines        int // recent lines looked at (web logs)
	WithDuration int // of which carry the request duration
}

func fileCheckItems(kind string, f fileFacts) []protocol.CheckItem {
	if !f.Readable {
		// Not created yet is where every install starts: amber, with the line to add. A file that
		// is there but closed to the agent is a real error: red, with the permission to fix.
		if f.Missing {
			return []protocol.CheckItem{item("readable", StatusWaiting, false, "not_created_yet", f.ReadError)}
		}
		return []protocol.CheckItem{item("readable", StatusError, false, "unreadable", f.ReadError)}
	}
	out := []protocol.CheckItem{item("readable", StatusOK, false, "", "")}
	switch kind {
	case "nginx", "apache", "caddy":
		switch {
		case f.Lines == 0:
			out = append(out, item("duration", StatusWaiting, false, "no_requests_yet", ""))
		case f.WithDuration == 0:
			out = append(out, item("duration", StatusError, false, "no_duration", ""))
		default:
			out = append(out, item("duration", StatusOK, false, "", ""))
		}
	}
	return out
}

// hostCheckItems describes what the agent can read about the machine itself: /proc, and whether
// it may see which process holds each socket (the map draws arrows from the real service only
// when it can). That second one needs CAP_SYS_PTRACE next to CAP_DAC_READ_SEARCH, or root.
func hostCheckItems(procRoot string) []protocol.CheckItem {
	facts := plainFileFacts(filepath.Join(procRoot, "stat"))
	if !facts.Readable {
		return []protocol.CheckItem{item("readable", StatusError, false, "no_proc", facts.ReadError)}
	}
	out := []protocol.CheckItem{item("readable", StatusOK, false, "", "")}
	if canReadProcessSockets(procRoot) {
		return append(out, item("processes", StatusOK, true, "", ""))
	}
	return append(out, item("processes", StatusOff, true, "needs_capabilities", ""))
}

// costMeter measures what the agent costs the machine it watches, between one check and the
// next: its share of one core and its resident memory. Measured, not promised, and over a real
// window: the seconds right after a start are all startup cost and would read like 200%.
type costMeter struct {
	at    time.Time
	ticks float64
}

const minCostWindow = time.Minute

func (m *costMeter) read(procSelf string, now time.Time) map[string]string {
	ticks, rssKB, ok := readSelf(procSelf)
	if !ok {
		return nil
	}
	previous, since := m.ticks, now.Sub(m.at)
	if m.at.IsZero() || since < minCostWindow {
		if m.at.IsZero() {
			m.at, m.ticks = now, ticks
		}
		return nil
	}
	m.at, m.ticks = now, ticks

	const ticksPerSecond = 100 // Linux USER_HZ, constant on every supported platform
	percent := (ticks - previous) / ticksPerSecond / since.Seconds() * 100
	facts := map[string]string{"agent_cpu_percent": strconv.FormatFloat(percent, 'f', 2, 64)}
	if rssKB > 0 {
		facts["agent_memory_mb"] = strconv.Itoa(rssKB / 1024)
	}
	return facts
}

// readSelf returns the CPU ticks the agent has used and its resident memory in kB.
func readSelf(procSelf string) (float64, int, bool) {
	stat, err := os.ReadFile(filepath.Join(procSelf, "stat"))
	if err != nil {
		return 0, 0, false
	}
	// utime and stime are fields 14 and 15, after the (name) which may contain spaces.
	fields := strings.Fields(string(stat)[strings.LastIndex(string(stat), ")")+1:])
	if len(fields) < 13 {
		return 0, 0, false
	}
	user, err1 := strconv.ParseFloat(fields[11], 64)
	sys, err2 := strconv.ParseFloat(fields[12], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	rssKB := 0
	if status, err := os.ReadFile(filepath.Join(procSelf, "status")); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
				if kb, err := strconv.Atoi(strings.Fields(rest)[0]); err == nil {
					rssKB = kb
				}
			}
		}
	}
	return user + sys, rssKB, true
}

// canReadProcessSockets tries to read the sockets of the oldest process of the machine, which
// belongs to root: if that works, every connection can be attributed to its service.
func canReadProcessSockets(procRoot string) bool {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}
	tried := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name[0] < '0' || name[0] > '9' || tried >= 8 {
			continue
		}
		tried++
		fds, err := os.ReadDir(filepath.Join(procRoot, name, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(procRoot, name, "fd", fd.Name()))
			if err != nil {
				break // the directory can be listed but its links cannot be followed
			}
			if _, ok := socketInode(target); ok {
				return true
			}
		}
	}
	return false
}
