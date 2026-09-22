package agent

import (
	"math"
	"testing"
)

func TestCPUPercentFromProcStat(t *testing.T) {
	prev, ok := parseCPUStat("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 2 3")
	if !ok {
		t.Fatal("parse")
	}
	cur, _ := parseCPUStat("cpu  150 0 150 900 0 0 0 0 0 0\n")
	// busy +100 over total +200
	if got := cpuPercent(prev, cur); math.Abs(got-50) > 0.001 {
		t.Fatalf("got %v", got)
	}
}

func TestMeminfo(t *testing.T) {
	used, total := parseMeminfo("MemTotal:       16000000 kB\nMemFree:  1000 kB\nMemAvailable:    4000000 kB\n")
	if total != 16000000*1024 || used != 12000000*1024 {
		t.Fatalf("used %d total %d", used, total)
	}
}

func TestLoadavg(t *testing.T) {
	if got := parseLoadavg("0.75 0.50 0.25 1/300 1234\n"); got != 0.75 {
		t.Fatalf("got %v", got)
	}
}
