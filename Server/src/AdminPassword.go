package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) SetDefaultAdminPassword(pwd string) {
	if s == nil {
		return
	}
	s.adminPasswordMu.Lock()
	defer s.adminPasswordMu.Unlock()
	s.defaultAdminPassword = strings.TrimSpace(pwd)
}

func (s *Store) GetAdminPassword() string {
	if s == nil {
		return "root"
	}
	s.adminPasswordMu.RLock()
	defer s.adminPasswordMu.RUnlock()
	if s.adminPassword != "" {
		return s.adminPassword
	}
	if s.defaultAdminPassword != "" {
		return s.defaultAdminPassword
	}
	return "root"
}

func (s *Store) LoadAdminPassword() error {
	if s == nil {
		return nil
	}
	// 1. Try PostgreSQL if available
	if s.pg != nil {
		val, err := s.pg.GetSystemConfig("admin_password")
		if err == nil && len(val) > 0 {
			pwd := strings.TrimSpace(string(val))
			if pwd != "" {
				s.adminPasswordMu.Lock()
				s.adminPassword = pwd
				s.adminPasswordMu.Unlock()
				return nil
			}
		}
	}

	// 2. Try disk file
	if s.dataDir != "" {
		pwdFile := filepath.Join(s.dataDir, "admin_password.txt")
		if data, err := os.ReadFile(pwdFile); err == nil {
			pwd := strings.TrimSpace(string(data))
			if pwd != "" {
				s.adminPasswordMu.Lock()
				s.adminPassword = pwd
				s.adminPasswordMu.Unlock()
				return nil
			}
		}
	}

	return nil
}

func (s *Store) SetAdminPassword(newPwd string) error {
	if s == nil {
		return fmt.Errorf("store unavailable")
	}
	newPwd = strings.TrimSpace(newPwd)
	if newPwd == "" {
		return fmt.Errorf("密碼不可為空")
	}

	s.adminPasswordMu.Lock()
	s.adminPassword = newPwd
	s.adminPasswordMu.Unlock()

	// 1. PostgreSQL if available
	if s.pg != nil {
		_ = s.pg.SetSystemConfig("admin_password", []byte(newPwd))
	}

	// 2. Disk file
	if s.dataDir != "" {
		_ = os.MkdirAll(s.dataDir, 0755)
		pwdFile := filepath.Join(s.dataDir, "admin_password.txt")
		_ = os.WriteFile(pwdFile, []byte(newPwd), 0600)
	}

	// 3. Update agent.properties files if present
	updateAgentPropertiesPassword(newPwd)

	return nil
}

func updateAgentPropertiesPassword(newPwd string) {
	candidates := []string{"./agent.properties", "agent.properties", "../agent.properties"}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		m["default_password"] = newPwd
		if out, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = os.WriteFile(p, out, 0644)
		}
	}
}
