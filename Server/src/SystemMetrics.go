package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

// RawSample represents one 5-second sample
type RawSample struct {
	Timestamp time.Time
	CPUPct    float64
	RAMPct    float64
	RAMUsed   uint64
	RAMTotal  uint64
	DiskPct   float64
	DiskUsed  uint64
	DiskTotal uint64
	NetRxBps  float64
	NetTxBps  float64
}

// MinuteMetricPoint represents 1-minute averaged data
type MinuteMetricPoint struct {
	Time     string  `json:"time"` // "15:04" or "MM/DD 15:04" or "MM/DD"
	Val      float64 `json:"val,omitempty"`
	UsedGiB  float64 `json:"used_gib,omitempty"`
	TotalGiB float64 `json:"total_gib,omitempty"`
	RxBps    float64 `json:"rx_bps,omitempty"`
	TxBps    float64 `json:"tx_bps,omitempty"`
}

type DailyMetricFile struct {
	Date            string              `json:"date"`
	Metric          string              `json:"metric"`
	Unit            string              `json:"unit"`
	RawSamplesCount int                 `json:"raw_samples_count"`
	IntervalSeconds int                 `json:"interval_seconds"`
	RetentionDays   int                 `json:"retention_days"`
	Latest          map[string]any      `json:"latest,omitempty"`
	Series          []MinuteMetricPoint `json:"series"`
}

type SystemMetricsCollector struct {
	mu           sync.RWMutex
	dataDir      string
	stopCh       chan struct{}
	lastNetRx    uint64
	lastNetTx    uint64
	lastNetTime  time.Time
	hasNetPrev   bool
	minuteBuffer []RawSample
	rawCountDay  int
	currentDay   string

	latestSample RawSample
	hasLatest    bool
}

var globalMetricsCollector *SystemMetricsCollector

func StartSystemMetricsCollector(dataDir string) *SystemMetricsCollector {
	if dataDir == "" {
		dataDir = "./data"
	}

	c := &SystemMetricsCollector{
		dataDir:      dataDir,
		stopCh:       make(chan struct{}),
		minuteBuffer: make([]RawSample, 0, 15),
		currentDay:   time.Now().Format("20060102"),
	}

	// Ensure directories exist
	_ = os.MkdirAll(filepath.Join(dataDir, "cpu-usage"), 0755)
	_ = os.MkdirAll(filepath.Join(dataDir, "ram-usage"), 0755)
	_ = os.MkdirAll(filepath.Join(dataDir, "disk-usage"), 0755)
	_ = os.MkdirAll(filepath.Join(dataDir, "net-usage"), 0755)

	// Take initial sample
	c.sampleOnce()

	globalMetricsCollector = c

	go c.runLoop()
	return c
}

func (c *SystemMetricsCollector) Stop() {
	if c == nil || c.stopCh == nil {
		return
	}
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *SystemMetricsCollector) runLoop() {
	ticker5s := time.NewTicker(5 * time.Second)
	defer ticker5s.Stop()

	pruneTicker := time.NewTicker(12 * time.Hour)
	defer pruneTicker.Stop()

	lastMinuteKey := time.Now().Format("15:04")

	for {
		select {
		case <-c.stopCh:
			return
		case <-pruneTicker.C:
			c.pruneOldFiles(90)
		case now := <-ticker5s.C:
			sample := c.sampleOnce()
			currentMinuteKey := now.Format("15:04")
			currentDayKey := now.Format("20060102")

			c.mu.Lock()
			if currentDayKey != c.currentDay {
				c.currentDay = currentDayKey
				c.rawCountDay = 0
			}
			c.rawCountDay++
			c.minuteBuffer = append(c.minuteBuffer, sample)

			if currentMinuteKey != lastMinuteKey && len(c.minuteBuffer) > 0 {
				samplesToFlush := c.minuteBuffer
				c.minuteBuffer = make([]RawSample, 0, 15)
				minuteToRecord := lastMinuteKey
				lastMinuteKey = currentMinuteKey
				rawCount := c.rawCountDay
				latest := c.latestSample
				c.mu.Unlock()

				c.flushMinuteAverage(currentDayKey, minuteToRecord, samplesToFlush, rawCount, latest)
			} else {
				c.mu.Unlock()
			}
		}
	}
}

func (c *SystemMetricsCollector) sampleOnce() RawSample {
	now := time.Now()
	var s RawSample
	s.Timestamp = now

	// 1. CPU
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		s.CPUPct = math.Round(pcts[0]*10) / 10
	}

	// 2. RAM
	if vmem, err := mem.VirtualMemory(); err == nil && vmem != nil {
		s.RAMPct = math.Round(vmem.UsedPercent*10) / 10
		s.RAMUsed = vmem.Used
		s.RAMTotal = vmem.Total
	}

	// 3. Disk (Cross-OS dynamic volume resolution for Windows, macOS, Linux)
	targetDisk := resolveDiskMount(c.dataDir)
	if dusage, err := disk.Usage(targetDisk); err == nil && dusage != nil {
		s.DiskPct = math.Round(dusage.UsedPercent*10) / 10
		s.DiskUsed = dusage.Used
		s.DiskTotal = dusage.Total
	} else if runtime.GOOS == "windows" && targetDisk != `C:\` {
		// Fallback to C:\ if custom drive path is not supported by API
		if dusageFallback, errFallback := disk.Usage(`C:\`); errFallback == nil && dusageFallback != nil {
			s.DiskPct = math.Round(dusageFallback.UsedPercent*10) / 10
			s.DiskUsed = dusageFallback.Used
			s.DiskTotal = dusageFallback.Total
		}
	}

	// 4. Net IO (Aggregate NIC counters across all platforms)
	if netIO, err := net.IOCounters(false); err == nil && len(netIO) > 0 {
		currRx := netIO[0].BytesRecv
		currTx := netIO[0].BytesSent
		if !c.lastNetTime.IsZero() && c.hasNetPrev {
			dt := now.Sub(c.lastNetTime).Seconds()
			if dt > 0 && currRx >= c.lastNetRx && currTx >= c.lastNetTx {
				s.NetRxBps = float64(currRx-c.lastNetRx) / dt
				s.NetTxBps = float64(currTx-c.lastNetTx) / dt
			}
		}
		c.lastNetRx = currRx
		c.lastNetTx = currTx
		c.lastNetTime = now
		c.hasNetPrev = true
	}

	c.mu.Lock()
	c.latestSample = s
	c.hasLatest = true
	c.mu.Unlock()

	return s
}

func (c *SystemMetricsCollector) flushMinuteAverage(dayKey, minuteKey string, samples []RawSample, rawCount int, latest RawSample) {
	if len(samples) == 0 {
		return
	}

	var sumCPU, sumRAM, sumDisk, sumRx, sumTx float64
	var lastRAMUsed, lastRAMTotal, lastDiskUsed, lastDiskTotal uint64

	for _, s := range samples {
		sumCPU += s.CPUPct
		sumRAM += s.RAMPct
		sumDisk += s.DiskPct
		sumRx += s.NetRxBps
		sumTx += s.NetTxBps
		if s.RAMTotal > 0 {
			lastRAMUsed = s.RAMUsed
			lastRAMTotal = s.RAMTotal
		}
		if s.DiskTotal > 0 {
			lastDiskUsed = s.DiskUsed
			lastDiskTotal = s.DiskTotal
		}
	}

	n := float64(len(samples))
	avgCPU := math.Round((sumCPU/n)*10) / 10
	avgRAM := math.Round((sumRAM/n)*10) / 10
	avgDisk := math.Round((sumDisk/n)*10) / 10
	avgRx := math.Round(sumRx / n)
	avgTx := math.Round(sumTx / n)

	ramUsedGiB := math.Round(float64(lastRAMUsed)/(1024*1024*1024)*100) / 100
	ramTotalGiB := math.Round(float64(lastRAMTotal)/(1024*1024*1024)*10) / 10
	diskUsedGiB := math.Round(float64(lastDiskUsed)/(1024*1024*1024)*10) / 10
	diskTotalGiB := math.Round(float64(lastDiskTotal)/(1024*1024*1024)*10) / 10

	latestTimeStr := latest.Timestamp.Format("15:04:05")

	// 1. CPU
	c.appendToDailyFile("cpu-usage", dayKey, "cpu", "%", rawCount, map[string]any{
		"time":  latestTimeStr,
		"value": latest.CPUPct,
	}, MinuteMetricPoint{Time: minuteKey, Val: avgCPU})

	// 2. RAM
	c.appendToDailyFile("ram-usage", dayKey, "ram", "%", rawCount, map[string]any{
		"time":      latestTimeStr,
		"percent":   latest.RAMPct,
		"used_gib":  ramUsedGiB,
		"total_gib": ramTotalGiB,
	}, MinuteMetricPoint{Time: minuteKey, Val: avgRAM, UsedGiB: ramUsedGiB, TotalGiB: ramTotalGiB})

	// 3. Disk
	c.appendToDailyFile("disk-usage", dayKey, "disk", "%", rawCount, map[string]any{
		"time":      latestTimeStr,
		"percent":   latest.DiskPct,
		"used_gib":  diskUsedGiB,
		"total_gib": diskTotalGiB,
	}, MinuteMetricPoint{Time: minuteKey, Val: avgDisk, UsedGiB: diskUsedGiB, TotalGiB: diskTotalGiB})

	// 4. Net
	c.appendToDailyFile("net-usage", dayKey, "net", "bytes/sec", rawCount, map[string]any{
		"time":    latestTimeStr,
		"rx_bps":  latest.NetRxBps,
		"tx_bps":  latest.NetTxBps,
		"rx_rate": formatBytesRate(latest.NetRxBps),
		"tx_rate": formatBytesRate(latest.NetTxBps),
	}, MinuteMetricPoint{Time: minuteKey, RxBps: avgRx, TxBps: avgTx})
}

func (c *SystemMetricsCollector) appendToDailyFile(folder, dayKey, metric, unit string, rawCount int, latest map[string]any, pt MinuteMetricPoint) {
	dir := filepath.Join(c.dataDir, folder)
	_ = os.MkdirAll(dir, 0755)
	filePath := filepath.Join(dir, dayKey+".json")

	var file DailyMetricFile
	if data, err := os.ReadFile(filePath); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &file)
	}

	file.Date = dayKey
	file.Metric = metric
	file.Unit = unit
	file.RawSamplesCount = rawCount
	file.IntervalSeconds = 60
	file.RetentionDays = 90
	file.Latest = latest

	found := false
	for i, existing := range file.Series {
		if existing.Time == pt.Time {
			file.Series[i] = pt
			found = true
			break
		}
	}
	if !found {
		file.Series = append(file.Series, pt)
	}

	sort.Slice(file.Series, func(i, j int) bool {
		return file.Series[i].Time < file.Series[j].Time
	})

	bytes, err := json.MarshalIndent(file, "", "  ")
	if err == nil {
		_ = os.WriteFile(filePath, bytes, 0644)
	}
}

func (c *SystemMetricsCollector) pruneOldFiles(days int) {
	cutoff := time.Now().AddDate(0, 0, -days).Format("20060102")
	folders := []string{"cpu-usage", "ram-usage", "disk-usage", "net-usage"}
	for _, folder := range folders {
		dir := filepath.Join(c.dataDir, folder)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".json")
			if len(name) == 8 && name < cutoff {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
}

func resolveDiskMount(dataDir string) string {
	if runtime.GOOS == "windows" {
		if abs, err := filepath.Abs(dataDir); err == nil {
			vol := filepath.VolumeName(abs)
			if vol != "" {
				return vol + `\`
			}
		}
		if drive := os.Getenv("SystemDrive"); drive != "" {
			return drive + `\`
		}
		return `C:\`
	}
	return "/"
}

func formatBytesRate(bps float64) string {
	if bps < 1024 {
		return fmt.Sprintf("%.0f B/s", bps)
	} else if bps < 1024*1024 {
		return fmt.Sprintf("%.0f KiB/s", bps/1024)
	} else if bps < 1024*1024*1024 {
		return fmt.Sprintf("%.2f MiB/s", bps/(1024*1024))
	}
	return fmt.Sprintf("%.2f GiB/s", bps/(1024*1024*1024))
}

// SystemMetricsResponse is returned by the HTTP API
type SystemMetricsResponse struct {
	OK              bool                `json:"ok"`
	Mode            string              `json:"mode"`  // "day", "week", "month"
	Date            string              `json:"date"`  // "YYYY-MM-DD"
	DateKey         string              `json:"date_key"`
	Range           string              `json:"range,omitempty"` // e.g. "2026-08-28 ~ 2026-09-03"
	RawSamplesCount int                 `json:"raw_samples_count"`
	IntervalSeconds int                 `json:"interval_seconds"`
	RetentionDays   int                 `json:"retention_days"`
	Latest          map[string]any      `json:"latest"`
	CPU             []MinuteMetricPoint `json:"cpu"`
	RAM             []MinuteMetricPoint `json:"ram"`
	Disk            []MinuteMetricPoint `json:"disk"`
	Net             []MinuteMetricPoint `json:"net"`
}

func (c *SystemMetricsCollector) QueryMetrics(dateStr, mode string) *SystemMetricsResponse {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "day"
	}

	cleanDate := strings.ReplaceAll(strings.TrimSpace(dateStr), "-", "")
	now := time.Now()
	var targetTime time.Time
	if len(cleanDate) == 8 {
		if t, err := time.Parse("20060102", cleanDate); err == nil {
			targetTime = t
		}
	}
	if targetTime.IsZero() {
		targetTime = now
		cleanDate = now.Format("20060102")
	}

	switch mode {
	case "week":
		return c.queryWeekMetrics(targetTime)
	case "month":
		return c.queryMonthMetrics(targetTime)
	default:
		return c.queryDayMetrics(cleanDate)
	}
}

func (c *SystemMetricsCollector) queryDayMetrics(cleanDate string) *SystemMetricsResponse {
	displayDate := cleanDate
	if len(cleanDate) == 8 {
		displayDate = fmt.Sprintf("%s-%s-%s", cleanDate[0:4], cleanDate[4:6], cleanDate[6:8])
	}

	resp := &SystemMetricsResponse{
		OK:              true,
		Mode:            "day",
		Date:            displayDate,
		DateKey:         cleanDate,
		Range:           displayDate,
		IntervalSeconds: 60,
		RetentionDays:   90,
		Latest:          map[string]any{},
		CPU:             []MinuteMetricPoint{},
		RAM:             []MinuteMetricPoint{},
		Disk:            []MinuteMetricPoint{},
		Net:             []MinuteMetricPoint{},
	}

	if cpuFile, err := c.readDailyFile("cpu-usage", cleanDate); err == nil {
		resp.CPU = cpuFile.Series
		if cpuFile.RawSamplesCount > resp.RawSamplesCount {
			resp.RawSamplesCount = cpuFile.RawSamplesCount
		}
	}
	if ramFile, err := c.readDailyFile("ram-usage", cleanDate); err == nil {
		resp.RAM = ramFile.Series
		if ramFile.RawSamplesCount > resp.RawSamplesCount {
			resp.RawSamplesCount = ramFile.RawSamplesCount
		}
	}
	if diskFile, err := c.readDailyFile("disk-usage", cleanDate); err == nil {
		resp.Disk = diskFile.Series
		if diskFile.RawSamplesCount > resp.RawSamplesCount {
			resp.RawSamplesCount = diskFile.RawSamplesCount
		}
	}
	if netFile, err := c.readDailyFile("net-usage", cleanDate); err == nil {
		resp.Net = netFile.Series
		if netFile.RawSamplesCount > resp.RawSamplesCount {
			resp.RawSamplesCount = netFile.RawSamplesCount
		}
	}

	c.populateLatest(resp, cleanDate)
	return resp
}

func (c *SystemMetricsCollector) queryWeekMetrics(endDay time.Time) *SystemMetricsResponse {
	startDate := endDay.AddDate(0, 0, -6)
	resp := &SystemMetricsResponse{
		OK:              true,
		Mode:            "week",
		Date:            endDay.Format("2006-01-02"),
		DateKey:         endDay.Format("20060102"),
		Range:           fmt.Sprintf("%s ~ %s", startDate.Format("2006-01-02"), endDay.Format("2006-01-02")),
		IntervalSeconds: 3600,
		RetentionDays:   90,
		Latest:          map[string]any{},
		CPU:             make([]MinuteMetricPoint, 0, 168),
		RAM:             make([]MinuteMetricPoint, 0, 168),
		Disk:            make([]MinuteMetricPoint, 0, 168),
		Net:             make([]MinuteMetricPoint, 0, 168),
	}

	for i := 0; i < 7; i++ {
		cur := startDate.AddDate(0, 0, i)
		dayKey := cur.Format("20060102")
		datePrefix := cur.Format("01/02")

		cpuFile, _ := c.readDailyFile("cpu-usage", dayKey)
		ramFile, _ := c.readDailyFile("ram-usage", dayKey)
		diskFile, _ := c.readDailyFile("disk-usage", dayKey)
		netFile, _ := c.readDailyFile("net-usage", dayKey)

		if cpuFile != nil {
			resp.RawSamplesCount += cpuFile.RawSamplesCount
		}

		for h := 0; h < 24; h++ {
			hourStr := fmt.Sprintf("%02d", h)
			timeLabel := fmt.Sprintf("%s %s:00", datePrefix, hourStr)

			// CPU
			if cpuFile != nil {
				var sum float64
				var cnt int
				for _, pt := range cpuFile.Series {
					if strings.HasPrefix(pt.Time, hourStr+":") {
						sum += pt.Val
						cnt++
					}
				}
				if cnt > 0 {
					resp.CPU = append(resp.CPU, MinuteMetricPoint{Time: timeLabel, Val: math.Round(sum/float64(cnt)*10) / 10})
				} else {
					resp.CPU = append(resp.CPU, MinuteMetricPoint{Time: timeLabel, Val: 0})
				}
			} else {
				resp.CPU = append(resp.CPU, MinuteMetricPoint{Time: timeLabel, Val: 0})
			}

			// RAM
			if ramFile != nil {
				var sum, used, total float64
				var cnt int
				for _, pt := range ramFile.Series {
					if strings.HasPrefix(pt.Time, hourStr+":") {
						sum += pt.Val
						used = pt.UsedGiB
						total = pt.TotalGiB
						cnt++
					}
				}
				if cnt > 0 {
					resp.RAM = append(resp.RAM, MinuteMetricPoint{Time: timeLabel, Val: math.Round(sum/float64(cnt)*10) / 10, UsedGiB: used, TotalGiB: total})
				} else {
					resp.RAM = append(resp.RAM, MinuteMetricPoint{Time: timeLabel, Val: 0})
				}
			} else {
				resp.RAM = append(resp.RAM, MinuteMetricPoint{Time: timeLabel, Val: 0})
			}

			// Disk
			if diskFile != nil {
				var sum, used, total float64
				var cnt int
				for _, pt := range diskFile.Series {
					if strings.HasPrefix(pt.Time, hourStr+":") {
						sum += pt.Val
						used = pt.UsedGiB
						total = pt.TotalGiB
						cnt++
					}
				}
				if cnt > 0 {
					resp.Disk = append(resp.Disk, MinuteMetricPoint{Time: timeLabel, Val: math.Round(sum/float64(cnt)*10) / 10, UsedGiB: used, TotalGiB: total})
				} else {
					resp.Disk = append(resp.Disk, MinuteMetricPoint{Time: timeLabel, Val: 0})
				}
			} else {
				resp.Disk = append(resp.Disk, MinuteMetricPoint{Time: timeLabel, Val: 0})
			}

			// Net
			if netFile != nil {
				var sumRx, sumTx float64
				var cnt int
				for _, pt := range netFile.Series {
					if strings.HasPrefix(pt.Time, hourStr+":") {
						sumRx += pt.RxBps
						sumTx += pt.TxBps
						cnt++
					}
				}
				if cnt > 0 {
					resp.Net = append(resp.Net, MinuteMetricPoint{Time: timeLabel, RxBps: math.Round(sumRx / float64(cnt)), TxBps: math.Round(sumTx / float64(cnt))})
				} else {
					resp.Net = append(resp.Net, MinuteMetricPoint{Time: timeLabel, RxBps: 0, TxBps: 0})
				}
			} else {
				resp.Net = append(resp.Net, MinuteMetricPoint{Time: timeLabel, RxBps: 0, TxBps: 0})
			}
		}
	}

	c.populateLatest(resp, endDay.Format("20060102"))
	return resp
}

func (c *SystemMetricsCollector) queryMonthMetrics(targetDate time.Time) *SystemMetricsResponse {
	year, month, _ := targetDate.Date()
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, targetDate.Location())
	lastDay := firstDay.AddDate(0, 1, -1)
	now := time.Now()

	resp := &SystemMetricsResponse{
		OK:              true,
		Mode:            "month",
		Date:            targetDate.Format("2006-01-02"),
		DateKey:         targetDate.Format("20060102"),
		Range:           fmt.Sprintf("%d 年 %d 月", year, int(month)),
		IntervalSeconds: 86400,
		RetentionDays:   90,
		Latest:          map[string]any{},
		CPU:             make([]MinuteMetricPoint, 0, 31),
		RAM:             make([]MinuteMetricPoint, 0, 31),
		Disk:            make([]MinuteMetricPoint, 0, 31),
		Net:             make([]MinuteMetricPoint, 0, 31),
	}

	daysInMonth := lastDay.Day()
	for d := 1; d <= daysInMonth; d++ {
		cur := time.Date(year, month, d, 0, 0, 0, 0, targetDate.Location())
		if cur.After(now) {
			break
		}
		dayKey := cur.Format("20060102")
		dateLabel := cur.Format("01/02")

		cpuFile, _ := c.readDailyFile("cpu-usage", dayKey)
		ramFile, _ := c.readDailyFile("ram-usage", dayKey)
		diskFile, _ := c.readDailyFile("disk-usage", dayKey)
		netFile, _ := c.readDailyFile("net-usage", dayKey)

		if cpuFile != nil {
			resp.RawSamplesCount += cpuFile.RawSamplesCount
		}

		if cpuFile != nil && len(cpuFile.Series) > 0 {
			var sum float64
			for _, pt := range cpuFile.Series {
				sum += pt.Val
			}
			resp.CPU = append(resp.CPU, MinuteMetricPoint{Time: dateLabel, Val: math.Round(sum/float64(len(cpuFile.Series))*10) / 10})
		} else {
			resp.CPU = append(resp.CPU, MinuteMetricPoint{Time: dateLabel, Val: 0})
		}

		if ramFile != nil && len(ramFile.Series) > 0 {
			var sum, used, total float64
			for _, pt := range ramFile.Series {
				sum += pt.Val
				used = pt.UsedGiB
				total = pt.TotalGiB
			}
			resp.RAM = append(resp.RAM, MinuteMetricPoint{Time: dateLabel, Val: math.Round(sum/float64(len(ramFile.Series))*10) / 10, UsedGiB: used, TotalGiB: total})
		} else {
			resp.RAM = append(resp.RAM, MinuteMetricPoint{Time: dateLabel, Val: 0})
		}

		if diskFile != nil && len(diskFile.Series) > 0 {
			var sum, used, total float64
			for _, pt := range diskFile.Series {
				sum += pt.Val
				used = pt.UsedGiB
				total = pt.TotalGiB
			}
			resp.Disk = append(resp.Disk, MinuteMetricPoint{Time: dateLabel, Val: math.Round(sum/float64(len(diskFile.Series))*10) / 10, UsedGiB: used, TotalGiB: total})
		} else {
			resp.Disk = append(resp.Disk, MinuteMetricPoint{Time: dateLabel, Val: 0})
		}

		if netFile != nil && len(netFile.Series) > 0 {
			var sumRx, sumTx float64
			for _, pt := range netFile.Series {
				sumRx += pt.RxBps
				sumTx += pt.TxBps
			}
			resp.Net = append(resp.Net, MinuteMetricPoint{Time: dateLabel, RxBps: math.Round(sumRx / float64(len(netFile.Series))), TxBps: math.Round(sumTx / float64(len(netFile.Series)))})
		} else {
			resp.Net = append(resp.Net, MinuteMetricPoint{Time: dateLabel, RxBps: 0, TxBps: 0})
		}
	}

	c.populateLatest(resp, targetDate.Format("20060102"))
	return resp
}

func (c *SystemMetricsCollector) populateLatest(resp *SystemMetricsResponse, dayKey string) {
	c.mu.RLock()
	latest := c.latestSample
	hasLatest := c.hasLatest
	rawCount := c.rawCountDay
	c.mu.RUnlock()

	nowDay := time.Now().Format("20060102")

	if hasLatest && dayKey == nowDay {
		if rawCount > resp.RawSamplesCount {
			resp.RawSamplesCount = rawCount
		}
		ramUsedGiB := math.Round(float64(latest.RAMUsed)/(1024*1024*1024)*100) / 100
		ramTotalGiB := math.Round(float64(latest.RAMTotal)/(1024*1024*1024)*10) / 10
		diskUsedGiB := math.Round(float64(latest.DiskUsed)/(1024*1024*1024)*10) / 10
		diskTotalGiB := math.Round(float64(latest.DiskTotal)/(1024*1024*1024)*10) / 10

		resp.Latest = map[string]any{
			"time":          latest.Timestamp.Format("15:04:05"),
			"cpu_pct":       latest.CPUPct,
			"gpu_pct":       nil,
			"ram_pct":       latest.RAMPct,
			"ram_used_gib":  ramUsedGiB,
			"ram_total_gib": ramTotalGiB,
			"disk_pct":      latest.DiskPct,
			"disk_used_gib": diskUsedGiB,
			"disk_total_gib": diskTotalGiB,
			"net_rx_rate":   formatBytesRate(latest.NetRxBps),
			"net_tx_rate":   formatBytesRate(latest.NetTxBps),
			"net_rx_bps":    latest.NetRxBps,
			"net_tx_bps":    latest.NetTxBps,
		}
	} else {
		if len(resp.CPU) > 0 {
			lastPt := resp.CPU[len(resp.CPU)-1]
			resp.Latest["cpu_pct"] = lastPt.Val
			resp.Latest["time"] = lastPt.Time
		}
		if len(resp.RAM) > 0 {
			lastPt := resp.RAM[len(resp.RAM)-1]
			resp.Latest["ram_pct"] = lastPt.Val
			resp.Latest["ram_used_gib"] = lastPt.UsedGiB
			resp.Latest["ram_total_gib"] = lastPt.TotalGiB
		}
		if len(resp.Disk) > 0 {
			lastPt := resp.Disk[len(resp.Disk)-1]
			resp.Latest["disk_pct"] = lastPt.Val
			resp.Latest["disk_used_gib"] = lastPt.UsedGiB
			resp.Latest["disk_total_gib"] = lastPt.TotalGiB
		}
		if len(resp.Net) > 0 {
			lastPt := resp.Net[len(resp.Net)-1]
			resp.Latest["net_rx_rate"] = formatBytesRate(lastPt.RxBps)
			resp.Latest["net_tx_rate"] = formatBytesRate(lastPt.TxBps)
			resp.Latest["net_rx_bps"] = lastPt.RxBps
			resp.Latest["net_tx_bps"] = lastPt.TxBps
		}
	}
}

func (c *SystemMetricsCollector) readDailyFile(folder, dayKey string) (*DailyMetricFile, error) {
	filePath := filepath.Join(c.dataDir, folder, dayKey+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var f DailyMetricFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
