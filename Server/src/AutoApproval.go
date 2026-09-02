package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

type autoApprovalDiskConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

func (s *Store) autoApprovalConfigPath() string {
	if s.dataDir != "" {
		return filepath.Join(s.dataDir, "auto_approval.json")
	}
	return "./data/auto_approval.json"
}

func (s *Store) LoadAutoApprovalConfig() error {
	if s == nil {
		return nil
	}

	// 1. Try PostgreSQL if available
	if s.pg != nil {
		val, err := s.pg.GetSystemConfig("auto_approval")
		if err == nil && len(val) > 0 {
			var config autoApprovalDiskConfig
			if err := json.Unmarshal(val, &config); err == nil {
				if config.IntervalMinutes <= 0 {
					config.IntervalMinutes = 1
				}
				s.autoApprovalMu.Lock()
				s.autoApprovalEnabled = config.Enabled
				s.autoApprovalIntervalMin = config.IntervalMinutes
				s.autoApprovalMu.Unlock()
				return nil
			}
		}
	}

	// 2. Try Disk file
	b, err := os.ReadFile(s.autoApprovalConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			s.autoApprovalMu.Lock()
			s.autoApprovalIntervalMin = 1
			s.autoApprovalMu.Unlock()
			return nil
		}
		return err
	}
	var config autoApprovalDiskConfig
	if err := json.Unmarshal(b, &config); err != nil {
		return err
	}
	if config.IntervalMinutes <= 0 {
		config.IntervalMinutes = 1
	}
	s.autoApprovalMu.Lock()
	s.autoApprovalEnabled = config.Enabled
	s.autoApprovalIntervalMin = config.IntervalMinutes
	s.autoApprovalMu.Unlock()
	return nil
}

func (s *Store) SaveAutoApprovalConfig() error {
	if s == nil {
		return fmt.Errorf("store not available")
	}

	s.autoApprovalMu.RLock()
	interval := s.autoApprovalIntervalMin
	if interval <= 0 {
		interval = 1
	}
	config := autoApprovalDiskConfig{
		Enabled:         s.autoApprovalEnabled,
		IntervalMinutes: interval,
	}
	s.autoApprovalMu.RUnlock()

	// 1. Save to PostgreSQL if available
	if s.pg != nil {
		if err := s.pg.SetSystemConfig("auto_approval", config); err != nil {
			Tools.Log.Print(Tools.LL_Warning, "save auto approval to postgres failed: %v", err)
		}
	}

	// 2. Save to Disk file
	filePath := s.autoApprovalConfigPath()
	dir := filepath.Dir(filePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, b, 0644)
}

func (s *Store) AutoApprovalEnabled() bool {
	if s == nil {
		return false
	}
	s.autoApprovalMu.RLock()
	defer s.autoApprovalMu.RUnlock()
	return s.autoApprovalEnabled
}

func (s *Store) AutoApprovalIntervalMinutes() int {
	if s == nil {
		return 1
	}
	s.autoApprovalMu.RLock()
	defer s.autoApprovalMu.RUnlock()
	if s.autoApprovalIntervalMin <= 0 {
		return 1
	}
	return s.autoApprovalIntervalMin
}

func (s *Store) SetAutoApprovalConfig(enabled bool, intervalMinutes int) error {
	if s == nil {
		return fmt.Errorf("store not available")
	}
	if intervalMinutes <= 0 {
		intervalMinutes = 1
	}
	s.autoApprovalMu.Lock()
	s.autoApprovalEnabled = enabled
	s.autoApprovalIntervalMin = intervalMinutes
	s.autoApprovalMu.Unlock()

	if err := s.SaveAutoApprovalConfig(); err != nil {
		return err
	}

	// If enabled, immediately trigger approval for any pending agents
	if enabled {
		go func() {
			if count, err := s.AutoApprovePendingAgents(); err != nil {
				Tools.Log.Print(Tools.LL_Error, "immediate agent approval failed: %v", err)
			} else if count > 0 {
				Tools.Log.Print(Tools.LL_Info, "Immediate agent approval completed: %d agent(s)", count)
			}
		}()
	}

	return nil
}

func (s *Store) SetAutoApprovalEnabled(enabled bool) error {
	return s.SetAutoApprovalConfig(enabled, s.AutoApprovalIntervalMinutes())
}

func StartAutoApprovalWorker(store *Store) func() {
	if store == nil {
		return nil
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		lastRun := time.Now()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if store.AutoApprovalEnabled() {
					interval := time.Duration(store.AutoApprovalIntervalMinutes()) * time.Minute
					if time.Since(lastRun) >= interval {
						lastRun = time.Now()
						if count, err := store.AutoApprovePendingAgents(); err != nil {
							Tools.Log.Print(Tools.LL_Error, "automatic agent approval failed: %v", err)
						} else if count > 0 {
							Tools.Log.Print(Tools.LL_Info, "Automatic agent approval completed: %d agent(s)", count)
						}
					}
				} else {
					lastRun = time.Now()
				}
			case <-stop:
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

func (s *Store) AutoApprovePendingAgents() (int, error) {
	if s == nil {
		return 0, fmt.Errorf("store not available")
	}
	entries := s.ListAgentRegistry()
	approved := 0
	for _, entry := range entries {
		if entry.Blocked || entry.Approved || strings.TrimSpace(entry.ClientID) == "" {
			continue
		}
		if _, err := issueApprovedAgentToken(s, entry.ClientID); err != nil {
			return approved, fmt.Errorf("approve %s: %w", entry.ClientID, err)
		}
		approved++
	}
	return approved, nil
}
