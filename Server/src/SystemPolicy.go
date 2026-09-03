package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type SystemPolicyConfig struct {
	VisitorTTLDays    int  `json:"visitor_ttl_days"`
	VisitorTTLEnabled bool `json:"visitor_ttl_enabled"`
	SoftDeleteEnabled bool `json:"soft_delete_enabled"`
}

func (s *Store) systemPolicyPath() string {
	if s.dataDir != "" {
		return filepath.Join(s.dataDir, "system_policy.json")
	}
	return "./data/system_policy.json"
}

func (s *Store) LoadSystemPolicy() error {
	if s == nil {
		return nil
	}

	// 1. Try PostgreSQL if available
	if s.pg != nil {
		val, err := s.pg.GetSystemConfig("system_policy")
		if err == nil && len(val) > 0 {
			var config SystemPolicyConfig
			if err := json.Unmarshal(val, &config); err == nil {
				s.applySystemPolicyConfigLocked(config)
				return nil
			}
		}
	}

	// 2. Try Disk file
	b, err := os.ReadFile(s.systemPolicyPath())
	if err != nil {
		if os.IsNotExist(err) {
			s.systemPolicyMu.Lock()
			s.visitorTTLDays = 15
			s.visitorTTLEnabled = true
			s.softDeleteEnabled = true
			s.systemPolicyLoaded = true
			s.systemPolicyMu.Unlock()
			return nil
		}
		return err
	}

	var config SystemPolicyConfig
	if err := json.Unmarshal(b, &config); err != nil {
		return err
	}
	s.applySystemPolicyConfigLocked(config)
	return nil
}

func (s *Store) applySystemPolicyConfigLocked(config SystemPolicyConfig) {
	if config.VisitorTTLDays <= 0 {
		config.VisitorTTLDays = 15
	}
	s.systemPolicyMu.Lock()
	defer s.systemPolicyMu.Unlock()
	s.visitorTTLDays = config.VisitorTTLDays
	s.visitorTTLEnabled = config.VisitorTTLEnabled
	s.softDeleteEnabled = config.SoftDeleteEnabled
	s.systemPolicyLoaded = true
}

func (s *Store) SaveSystemPolicy() error {
	if s == nil {
		return nil
	}

	cfg := s.GetSystemPolicy()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// 1. Save to PostgreSQL if available
	if s.pg != nil {
		_ = s.pg.SetSystemConfig("system_policy", b)
	}

	// 2. Save to Disk file
	path := s.systemPolicyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func (s *Store) GetSystemPolicy() SystemPolicyConfig {
	if s == nil {
		return SystemPolicyConfig{
			VisitorTTLDays:    15,
			VisitorTTLEnabled: true,
			SoftDeleteEnabled: true,
		}
	}
	s.systemPolicyMu.RLock()
	defer s.systemPolicyMu.RUnlock()

	ttlDays := s.visitorTTLDays
	if ttlDays <= 0 {
		ttlDays = 15
	}
	ttlEnabled := s.visitorTTLEnabled
	softDelete := s.softDeleteEnabled
	if !s.systemPolicyLoaded {
		ttlEnabled = true
		softDelete = true
	}
	return SystemPolicyConfig{
		VisitorTTLDays:    ttlDays,
		VisitorTTLEnabled: ttlEnabled,
		SoftDeleteEnabled: softDelete,
	}
}

func (s *Store) SetSystemPolicy(cfg SystemPolicyConfig) error {
	if s == nil {
		return nil
	}
	if cfg.VisitorTTLDays <= 0 {
		cfg.VisitorTTLDays = 15
	}
	s.systemPolicyMu.Lock()
	s.visitorTTLDays = cfg.VisitorTTLDays
	s.visitorTTLEnabled = cfg.VisitorTTLEnabled
	s.softDeleteEnabled = cfg.SoftDeleteEnabled
	s.systemPolicyLoaded = true
	s.systemPolicyMu.Unlock()

	return s.SaveSystemPolicy()
}

func (s *Store) VisitorTTL() time.Duration {
	cfg := s.GetSystemPolicy()
	if cfg.VisitorTTLDays <= 0 {
		return 15 * 24 * time.Hour
	}
	return time.Duration(cfg.VisitorTTLDays) * 24 * time.Hour
}

func (s *Store) VisitorTTLEnabled() bool {
	return s.GetSystemPolicy().VisitorTTLEnabled
}

func (s *Store) SoftDeleteEnabled() bool {
	return s.GetSystemPolicy().SoftDeleteEnabled
}
