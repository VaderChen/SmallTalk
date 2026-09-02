package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type VisitorData struct {
	DayKey                   string           `json:"day_key"`
	TodayVisitors            map[string]int64 `json:"today_visitors"`
	TodayVisitorsCount       int              `json:"today_visitors_count"`
	TotalHistoricalVisitors  int64            `json:"total_historical_visitors"`
	TodayPageViews           int64            `json:"today_page_views"`
	TotalHistoricalPageViews int64            `json:"total_historical_page_views"`
	LastUpdated              time.Time        `json:"last_updated"`
}

type VisitorTracker struct {
	mu                       sync.RWMutex
	dataDir                  string
	saveFile                 string
	dayKey                   string
	todayVisitors            map[string]int64
	todayVisitorsCount       int
	totalHistoricalVisitors  int64
	todayPageViews           int64
	totalHistoricalPageViews int64
	dirty                    bool
	stopWorker               chan struct{}
}

func NewVisitorTracker(dataDir string) *VisitorTracker {
	if dataDir == "" {
		dataDir = "./data"
	}
	saveDir := filepath.Join(dataDir, "stats")
	_ = os.MkdirAll(saveDir, 0755)
	saveFile := filepath.Join(saveDir, "visitors.json")

	now := time.Now()
	dayKey := now.Format("2006-01-02")

	vt := &VisitorTracker{
		dataDir:       dataDir,
		saveFile:      saveFile,
		dayKey:        dayKey,
		todayVisitors: make(map[string]int64),
		stopWorker:    make(chan struct{}),
	}

	vt.load()
	vt.startWorkers()
	return vt
}

func (vt *VisitorTracker) load() {
	if vt.saveFile == "" {
		return
	}
	raw, err := os.ReadFile(vt.saveFile)
	if err != nil {
		return
	}
	var data VisitorData
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}

	vt.mu.Lock()
	defer vt.mu.Unlock()

	now := time.Now()
	todayKey := now.Format("2006-01-02")

	vt.totalHistoricalVisitors = data.TotalHistoricalVisitors
	vt.totalHistoricalPageViews = data.TotalHistoricalPageViews

	if data.DayKey == todayKey {
		vt.dayKey = todayKey
		vt.todayVisitors = data.TodayVisitors
		if vt.todayVisitors == nil {
			vt.todayVisitors = make(map[string]int64)
		}
		vt.todayVisitorsCount = len(vt.todayVisitors)
		vt.todayPageViews = data.TodayPageViews
	} else {
		if data.DayKey != "" {
			vt.totalHistoricalVisitors += int64(len(data.TodayVisitors))
			vt.totalHistoricalPageViews += data.TodayPageViews
		}
		vt.dayKey = todayKey
		vt.todayVisitors = make(map[string]int64)
		vt.todayVisitorsCount = 0
		vt.todayPageViews = 0
		vt.dirty = true
	}
}

func (vt *VisitorTracker) checkDayRolloverLocked(now time.Time) {
	todayKey := now.Format("2006-01-02")
	if vt.dayKey == todayKey {
		return
	}
	vt.totalHistoricalVisitors += int64(len(vt.todayVisitors))
	vt.totalHistoricalPageViews += vt.todayPageViews
	vt.dayKey = todayKey
	vt.todayVisitors = make(map[string]int64)
	vt.todayVisitorsCount = 0
	vt.todayPageViews = 0
	vt.dirty = true
}

func (vt *VisitorTracker) RecordVisit(visitorKey string, isPageView bool) {
	if visitorKey == "" {
		return
	}
	now := time.Now()
	vt.mu.Lock()
	defer vt.mu.Unlock()

	vt.checkDayRolloverLocked(now)

	if isPageView {
		vt.todayPageViews++
		vt.dirty = true
	}

	if _, exists := vt.todayVisitors[visitorKey]; !exists {
		vt.todayVisitors[visitorKey] = now.Unix()
		vt.todayVisitorsCount = len(vt.todayVisitors)
		vt.dirty = true
	}
}

func (vt *VisitorTracker) GetStats(now time.Time) (todayUV int, totalUV int64, todayPV int64, totalPV int64) {
	vt.mu.Lock()
	defer vt.mu.Unlock()

	vt.checkDayRolloverLocked(now)

	todayUV = vt.todayVisitorsCount
	totalUV = vt.totalHistoricalVisitors + int64(todayUV)
	todayPV = vt.todayPageViews
	totalPV = vt.totalHistoricalPageViews + todayPV
	return
}

func (vt *VisitorTracker) Save() error {
	vt.mu.Lock()
	if !vt.dirty {
		vt.mu.Unlock()
		return nil
	}
	vt.dirty = false
	data := VisitorData{
		DayKey:                   vt.dayKey,
		TodayVisitors:            vt.todayVisitors,
		TodayVisitorsCount:       vt.todayVisitorsCount,
		TotalHistoricalVisitors:  vt.totalHistoricalVisitors,
		TodayPageViews:           vt.todayPageViews,
		TotalHistoricalPageViews: vt.totalHistoricalPageViews,
		LastUpdated:              time.Now(),
	}
	vt.mu.Unlock()

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := vt.saveFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, vt.saveFile)
}

func (vt *VisitorTracker) startWorkers() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				vt.mu.Lock()
				vt.checkDayRolloverLocked(time.Now())
				vt.mu.Unlock()
				_ = vt.Save()
			case <-vt.stopWorker:
				_ = vt.Save()
				return
			}
		}
	}()
}

func (vt *VisitorTracker) Close() {
	if vt.stopWorker != nil {
		close(vt.stopWorker)
	}
	_ = vt.Save()
}
