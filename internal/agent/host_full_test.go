package agent

import (
	"math"
	"testing"
	"time"
)

func TestHostCollectorReadsEverythingFromProc(t *testing.T) {
	h := &hostCollector{root: "testdata/proc", pageSize: 4096, clockTicks: 100}
	first, ok := h.sample(t0)
	if !ok {
		t.Fatal("sample")
	}
	if first.MemTotal != 8000000*1024 || first.MemUsed != 6000000*1024 {
		t.Fatalf("memory %+v", first)
	}
	if first.SwapTotal != 2000000*1024 || first.SwapUsed != 1500000*1024 {
		t.Fatalf("swap %+v", first)
	}
	if first.Load1 != 1.5 || first.Load5 != 1.2 || first.Load15 != 0.9 {
		t.Fatalf("load %+v", first)
	}
	if first.OOMKills != 0 {
		t.Fatal("counters need two samples")
	}

	// Second sample 60 s later: counters advanced.
	h.prevAt = t0.Add(-60 * time.Second)
	h.prevCounters.netRx -= 60 * 1000
	h.prevCounters.netTx -= 60 * 500
	h.prevCounters.diskRead -= 60 * 2048
	h.prevCounters.oomKills--
	h.prevCounters.swapOut = 0
	h.prevCPU = &cpuDetail{user: 500, system: 250, idle: 7500, iowait: 200, steal: 100}
	s, _ := h.sample(t0)

	if math.Abs(s.NetRxBps-1000) > 0.01 || math.Abs(s.NetTxBps-500) > 0.01 {
		t.Fatalf("network %+v (loopback and docker bridges excluded)", s)
	}
	if math.Abs(s.DiskReadBps-2048) > 0.01 {
		t.Fatalf("disk %+v (partitions and loop devices excluded)", s)
	}
	if s.OOMKills != 1 || s.SwapOutPages != 40 {
		t.Fatalf("oom/swap %+v", s)
	}
	// delta: user 500, system 250, idle 500, iowait 100, steal 50, softirq 50 -> total 1450
	if math.Abs(s.CPU-float64(850)/1450*100) > 0.1 || math.Abs(s.CPUIOWait-float64(100)/1450*100) > 0.1 || math.Abs(s.CPUSteal-float64(50)/1450*100) > 0.1 {
		t.Fatalf("cpu %+v", s)
	}
	if len(s.TopProcesses) != 2 || s.TopProcesses[0].Name != "mysqld" || s.TopProcesses[0].RSS != 262144*4096 {
		t.Fatalf("processes %+v", s.TopProcesses)
	}
}
