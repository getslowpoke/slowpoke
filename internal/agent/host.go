package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

type cpuTimes struct{ busy, total uint64 }

type cpuDetail struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuDetail) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

type counters struct {
	netRx, netTx, diskRead, diskWrite, oomKills, swapOut uint64
}

// hostCollector samples the machine from /proc (Linux).
type hostCollector struct {
	root         string // "/" or the host filesystem mounted inside a container
	pageSize     uint64
	clockTicks   float64
	prev         *cpuTimes
	prevCPU      *cpuDetail
	prevCounters counters
	prevAt       time.Time
	prevProcCPU  map[int]uint64
}

func newHostCollector() *hostCollector {
	root := os.Getenv("SLOWPOKE_HOST_ROOT")
	if root == "" {
		root = "/"
	}
	return &hostCollector{root: root, pageSize: uint64(os.Getpagesize()), clockTicks: 100}
}

func (h *hostCollector) path(p string) string {
	return filepath.Join(h.root, p)
}

func (h *hostCollector) read(p string) string {
	raw, err := os.ReadFile(h.path(p))
	if err != nil {
		return ""
	}
	return string(raw)
}

func (h *hostCollector) sample(now time.Time) (protocol.HostSample, bool) {
	s := protocol.HostSample{Minute: minuteOf(now), CPUs: runtime.NumCPU()}

	stat := h.read("proc/stat")
	if stat == "" {
		return s, false
	}
	if cur, ok := parseCPUDetail(stat); ok {
		if h.prevCPU != nil && cur.total() > h.prevCPU.total() {
			d := float64(cur.total() - h.prevCPU.total())
			idle := float64((cur.idle - h.prevCPU.idle) + (cur.iowait - h.prevCPU.iowait))
			s.CPU = (d - idle) / d * 100
			s.CPUIOWait = float64(cur.iowait-h.prevCPU.iowait) / d * 100
			s.CPUSteal = float64(cur.steal-h.prevCPU.steal) / d * 100
		}
		h.prevCPU = &cur
	}

	mem := parseKeyValues(h.read("proc/meminfo"))
	s.MemTotal = mem["MemTotal"] * 1024
	if avail, ok := mem["MemAvailable"]; ok && avail*1024 <= s.MemTotal {
		s.MemUsed = s.MemTotal - avail*1024
	} else {
		s.MemUsed = s.MemTotal - mem["MemFree"]*1024
	}
	s.SwapTotal = mem["SwapTotal"] * 1024
	s.SwapUsed = s.SwapTotal - min(s.SwapTotal, mem["SwapFree"]*1024)

	if f := strings.Fields(h.read("proc/loadavg")); len(f) >= 3 {
		s.Load1, _ = strconv.ParseFloat(f[0], 64)
		s.Load5, _ = strconv.ParseFloat(f[1], 64)
		s.Load15, _ = strconv.ParseFloat(f[2], 64)
	}

	var fs syscall.Statfs_t
	if err := syscall.Statfs(h.root, &fs); err == nil {
		s.DiskTotal = fs.Blocks * uint64(fs.Bsize)
		s.DiskUsed = (fs.Blocks - fs.Bfree) * uint64(fs.Bsize)
	}

	vm := parseKeyValues(h.read("proc/vmstat"))
	cur := counters{oomKills: vm["oom_kill"], swapOut: vm["pswpout"]}
	cur.netRx, cur.netTx = parseNetDev(h.read("proc/net/dev"))
	if cur.netRx == 0 && cur.netTx == 0 {
		cur.netRx, cur.netTx = parseNetDev(h.read("proc/net_dev"))
	}
	cur.diskRead, cur.diskWrite = parseDiskstats(h.read("proc/diskstats"))

	seconds := now.Sub(h.prevAt).Seconds()
	if !h.prevAt.IsZero() && seconds > 0 {
		rate := func(c, p uint64) float64 {
			if c < p {
				return 0
			}
			return float64(c-p) / seconds
		}
		delta := func(c, p uint64) uint64 {
			if c < p {
				return 0
			}
			return c - p
		}
		s.NetRxBps = rate(cur.netRx, h.prevCounters.netRx)
		s.NetTxBps = rate(cur.netTx, h.prevCounters.netTx)
		s.DiskReadBps = rate(cur.diskRead, h.prevCounters.diskRead)
		s.DiskWriteBps = rate(cur.diskWrite, h.prevCounters.diskWrite)
		s.OOMKills = delta(cur.oomKills, h.prevCounters.oomKills)
		s.SwapOutPages = delta(cur.swapOut, h.prevCounters.swapOut)
	}

	s.TopProcesses = h.topProcesses(seconds)
	h.prevCounters, h.prevAt = cur, now
	return s, true
}

func parseCPUDetail(content string) (cpuDetail, bool) {
	line, _, _ := strings.Cut(content, "\n")
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return cpuDetail{}, false
	}
	v := func(i int) uint64 {
		n, _ := strconv.ParseUint(f[i], 10, 64)
		return n
	}
	return cpuDetail{user: v(1), nice: v(2), system: v(3), idle: v(4), iowait: v(5), irq: v(6), softirq: v(7), steal: v(8)}, true
}

func parseKeyValues(content string) map[string]uint64 {
	out := map[string]uint64{}
	for _, line := range strings.Split(content, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			v, _ := strconv.ParseUint(f[1], 10, 64)
			out[strings.TrimSuffix(f[0], ":")] = v
		}
	}
	return out
}

func parseNetDev(content string) (rx, tx uint64) {
	for _, line := range strings.Split(content, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) >= 9 {
			r, _ := strconv.ParseUint(f[0], 10, 64)
			t, _ := strconv.ParseUint(f[8], 10, 64)
			rx += r
			tx += t
		}
	}
	return rx, tx
}

// parseDiskstats sums whole devices (no partitions, loop or ram devices), in bytes.
func parseDiskstats(content string) (read, write uint64) {
	for _, line := range strings.Split(content, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		name := f[2]
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") || isPartition(name) {
			continue
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		read += r * 512
		write += w * 512
	}
	return read, write
}

func isPartition(name string) bool {
	if strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk") {
		return strings.Contains(name, "p") && name[len(name)-1] >= '0' && name[len(name)-1] <= '9' && strings.LastIndex(name, "p") > strings.LastIndex(name, "n")
	}
	return len(name) > 0 && name[len(name)-1] >= '0' && name[len(name)-1] <= '9'
}

// topProcesses groups processes by name and returns the heaviest by memory and CPU.
func (h *hostCollector) topProcesses(seconds float64) []protocol.Process {
	entries, err := os.ReadDir(h.path("proc"))
	if err != nil {
		return nil
	}
	byName := map[string]*protocol.Process{}
	cpuNow := map[int]uint64{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		raw := h.read("proc/" + e.Name() + "/stat")
		open, close := strings.IndexByte(raw, '('), strings.LastIndexByte(raw, ')')
		if open < 0 || close < open {
			continue
		}
		name := raw[open+1 : close]
		f := strings.Fields(raw[close+1:])
		if len(f) < 22 {
			continue
		}
		utime, _ := strconv.ParseUint(f[11], 10, 64)
		stime, _ := strconv.ParseUint(f[12], 10, 64)
		rssPages, _ := strconv.ParseUint(f[21], 10, 64)
		cpuNow[pid] = utime + stime

		p := byName[name]
		if p == nil {
			p = &protocol.Process{Name: name}
			byName[name] = p
		}
		p.N++
		p.RSS += rssPages * h.pageSize
		if prev, ok := h.prevProcCPU[pid]; ok && seconds > 0 && utime+stime >= prev {
			p.CPU += float64(utime+stime-prev) / h.clockTicks / seconds * 100
		}
	}
	h.prevProcCPU = cpuNow

	list := make([]protocol.Process, 0, len(byName))
	for _, p := range byName {
		p.CPU = float64(int(p.CPU*10)) / 10
		list = append(list, *p)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CPU != list[j].CPU {
			return list[i].CPU > list[j].CPU
		}
		return list[i].RSS > list[j].RSS
	})
	byRSS := append([]protocol.Process(nil), list...)
	sort.Slice(byRSS, func(i, j int) bool { return byRSS[i].RSS > byRSS[j].RSS })

	seen := map[string]bool{}
	var out []protocol.Process
	for _, group := range [][]protocol.Process{byRSS, list} {
		for i := 0; i < len(group) && i < 5; i++ {
			if !seen[group[i].Name] {
				seen[group[i].Name] = true
				out = append(out, group[i])
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RSS > out[j].RSS })
	return out
}

// Kept for the simple CPU tests.
func parseCPUStat(content string) (cpuTimes, bool) {
	d, ok := parseCPUDetail(content)
	if !ok {
		return cpuTimes{}, false
	}
	idle := d.idle + d.iowait
	return cpuTimes{busy: d.total() - idle, total: d.total()}, true
}

func cpuPercent(prev, cur cpuTimes) float64 {
	if cur.total <= prev.total {
		return 0
	}
	return float64(cur.busy-prev.busy) / float64(cur.total-prev.total) * 100
}

func parseMeminfo(content string) (used, total uint64) {
	v := parseKeyValues(content)
	total = v["MemTotal"] * 1024
	if avail, ok := v["MemAvailable"]; ok && avail*1024 <= total {
		return total - avail*1024, total
	}
	return total - v["MemFree"]*1024, total
}

func parseLoadavg(content string) float64 {
	f := strings.Fields(content)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}
