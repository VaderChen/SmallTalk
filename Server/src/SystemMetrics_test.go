package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemMetricsCollector(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "metrics-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	collector := StartSystemMetricsCollector(tempDir)
	defer collector.Stop()

	time.Sleep(100 * time.Millisecond)

	todayKey := time.Now().Format("20060102")
	samples := []RawSample{
		{Timestamp: time.Now(), CPUPct: 12.5, RAMPct: 20.0, RAMUsed: 2 * 1024 * 1024 * 1024, RAMTotal: 16 * 1024 * 1024 * 1024, DiskPct: 45.0, DiskUsed: 45 * 1024 * 1024 * 1024, DiskTotal: 100 * 1024 * 1024 * 1024, NetRxBps: 10240, NetTxBps: 20480},
		{Timestamp: time.Now(), CPUPct: 13.5, RAMPct: 20.0, RAMUsed: 2 * 1024 * 1024 * 1024, RAMTotal: 16 * 1024 * 1024 * 1024, DiskPct: 45.0, DiskUsed: 45 * 1024 * 1024 * 1024, DiskTotal: 100 * 1024 * 1024 * 1024, NetRxBps: 20480, NetTxBps: 40960},
	}

	collector.flushMinuteAverage(todayKey, "12:00", samples, 2, samples[1])

	// Verify CPU file was created
	cpuFile := filepath.Join(tempDir, "cpu-usage", todayKey+".json")
	if _, err := os.Stat(cpuFile); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", cpuFile)
	}

	// 1. Query metrics Day mode
	respDay := collector.QueryMetrics(todayKey, "day")
	if !respDay.OK {
		t.Fatalf("expected respDay.OK to be true")
	}
	if len(respDay.CPU) == 0 {
		t.Fatalf("expected at least 1 CPU point")
	}
	if respDay.CPU[0].Val != 13.0 {
		t.Fatalf("expected avg CPU 13.0, got %v", respDay.CPU[0].Val)
	}

	// 2. Query metrics Week mode
	respWeek := collector.QueryMetrics(todayKey, "week")
	if !respWeek.OK || respWeek.Mode != "week" {
		t.Fatalf("expected respWeek.Mode to be week")
	}
	if len(respWeek.CPU) != 168 {
		t.Fatalf("expected 168 hours in week mode, got %d", len(respWeek.CPU))
	}

	// 3. Query metrics Month mode
	respMonth := collector.QueryMetrics(todayKey, "month")
	if !respMonth.OK || respMonth.Mode != "month" {
		t.Fatalf("expected respMonth.Mode to be month")
	}
	if len(respMonth.CPU) == 0 {
		t.Fatalf("expected at least 1 day in month mode")
	}
}
