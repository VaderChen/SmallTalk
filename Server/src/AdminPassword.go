package main

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const minAdminPasswordLength = 12

func hashAdminPassword(pwd string) (string, error) {
	pwd = strings.TrimSpace(pwd)
	if pwd == "" {
		return "", nil
	}
	if strings.HasPrefix(pwd, "$2a$") || strings.HasPrefix(pwd, "$2b$") || strings.HasPrefix(pwd, "$2y$") {
		return pwd, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	return string(hash), err
}

func (s *Store) SetDefaultAdminPassword(pwd string) {
	if s == nil {
		return
	}
	pwd = strings.TrimSpace(pwd)
	if pwd == "" || strings.EqualFold(pwd, "root") {
		return
	}
	s.adminPasswordMu.Lock()
	defer s.adminPasswordMu.Unlock()
	hash, err := hashAdminPassword(pwd)
	if err == nil {
		s.defaultAdminPassword = hash
	}
}

func (s *Store) GetAdminPassword() string {
	if s == nil {
		return ""
	}
	s.adminPasswordMu.RLock()
	defer s.adminPasswordMu.RUnlock()
	if s.adminPassword != "" {
		return s.adminPassword
	}
	if s.defaultAdminPassword != "" {
		return s.defaultAdminPassword
	}
	return ""
}

func (s *Store) VerifyAdminPassword(candidate string) bool {
	return verifyAdminPasswordValue(s.GetAdminPassword(), candidate)
}

func verifyAdminPasswordValue(stored, candidate string) bool {
	if stored == "" || candidate == "" {
		return false
	}
	if strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") || strings.HasPrefix(stored, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(candidate)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(candidate)) == 1
}

func (s *Store) LoadAdminPassword() error {
	if s == nil {
		return nil
	}
	// 1. Try PostgreSQL if available
	if s.pg != nil {
		val, err := s.pg.GetSystemConfig("admin_password")
		if err == nil && len(val) > 0 {
			raw := ""
			if jsonErr := json.Unmarshal(val, &raw); jsonErr != nil {
				raw = strings.TrimSpace(string(val))
			}
			if decoded, decodeErr := base64.StdEncoding.DecodeString(raw); decodeErr == nil && len(decoded) > 0 {
				raw = strings.TrimSpace(string(decoded))
			}
			pwd, hashErr := hashAdminPassword(raw)
			if hashErr != nil {
				return hashErr
			}
			if pwd != "" {
				s.adminPasswordMu.Lock()
				s.adminPassword = pwd
				s.adminPasswordMu.Unlock()
				if pwd != raw {
					if err := s.pg.SetSystemConfig("admin_password", pwd); err != nil {
						return err
					}
				}
				return nil
			}
		}
	}

	// 2. Try disk file
	if s.dataDir != "" {
		pwdFile := filepath.Join(s.dataDir, "admin_password.txt")
		if data, err := os.ReadFile(pwdFile); err == nil {
			raw := strings.TrimSpace(string(data))
			pwd, hashErr := hashAdminPassword(raw)
			if hashErr != nil {
				return hashErr
			}
			if pwd != "" {
				s.adminPasswordMu.Lock()
				s.adminPassword = pwd
				s.adminPasswordMu.Unlock()
				if pwd != raw {
					_ = writePrivateFile(pwdFile, []byte(pwd))
				}
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
	if len([]rune(newPwd)) < minAdminPasswordLength {
		return fmt.Errorf("密碼長度至少需 %d 個字元", minAdminPasswordLength)
	}
	hash, err := hashAdminPassword(newPwd)
	if err != nil {
		return fmt.Errorf("密碼雜湊失敗: %w", err)
	}

	// 1. PostgreSQL if available
	if s.pg != nil {
		if err := s.pg.SetSystemConfig("admin_password", hash); err != nil {
			return fmt.Errorf("儲存管理員密碼失敗: %w", err)
		}
	}

	// 2. Disk file
	if s.dataDir != "" {
		if err := os.MkdirAll(s.dataDir, 0755); err != nil {
			return fmt.Errorf("建立密碼資料目錄失敗: %w", err)
		}
		pwdFile := filepath.Join(s.dataDir, "admin_password.txt")
		if err := writePrivateFile(pwdFile, []byte(hash)); err != nil {
			return fmt.Errorf("儲存管理員密碼失敗: %w", err)
		}
	}
	s.adminPasswordMu.Lock()
	s.adminPassword = hash
	s.adminPasswordMu.Unlock()
	clearAgentPropertiesPassword()

	return nil
}

func clearAgentPropertiesPassword() {
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
		m["default_password"] = ""
		if out, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = writePrivateFile(p, out)
		}
	}
}
