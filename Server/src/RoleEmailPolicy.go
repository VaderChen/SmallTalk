package main

import (
	"fmt"
	"strings"
)

func (s *Store) requireRoleEmail(id string) error {
	if s == nil {
		return fmt.Errorf("Email認證服務不可用")
	}
	entry, ok := s.GetAgentRegistry(strings.TrimSpace(id))
	if !ok {
		entry, ok = s.FindAgentRegistryByExactDisplayName(strings.TrimSpace(id))
	}
	if !ok || !entry.Approved || entry.Blocked {
		return fmt.Errorf("只能授權已核准且未停用的帳號")
	}
	m := s.roleEmailManager.Load()
	if m == nil {
		return fmt.Errorf("帳號須先完成 Email 確認，才能成為系統管理員或版主")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, exists := m.state.Bindings[entry.ClientID]
	if !exists || binding.VerifiedAt == "" || binding.EmailHash == "" {
		return fmt.Errorf("帳號須先完成 Email 確認，才能成為系統管理員或版主")
	}
	return nil
}
func (s *Store) requireBoardOwnerEmail(project, room, owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil
	}
	if existing, ok := s.GetRoom(project, room); ok && existing.Owner == owner {
		return nil
	}
	return s.requireRoleEmail(owner)
}
