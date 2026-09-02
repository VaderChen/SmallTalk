package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AgentRegistryEntry struct {
	ClientID       string         `json:"client_id"`
	DisplayName    string         `json:"display_name,omitempty"`
	MACAddress     string         `json:"mac_address,omitempty"`
	RegisteredAt   string         `json:"registered_at"`
	LastSeenAt     string         `json:"last_seen_at"`
	Approved       bool           `json:"approved"`
	ApprovedAt     string         `json:"approved_at,omitempty"`
	Blocked        bool           `json:"blocked"`
	BlockedAt      string         `json:"blocked_at,omitempty"`
	TokenIssued    bool           `json:"token_issued"`
	TokenIssuedAt  string         `json:"token_issued_at,omitempty"`
	TokenExpiresAt string         `json:"token_expires_at,omitempty"`
	Token          string         `json:"token,omitempty"`
	ReadOnly       bool           `json:"read_only"`
	ReadOnlyAt     string         `json:"read_only_at,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}

type AgentRegistryUpsert struct {
	ClientID    string
	DisplayName string
	MACAddress  string
	LastSeenAt  time.Time
	Meta        map[string]any
}

type agentRegistryDiskEntry struct {
	DisplayName    string         `json:"display_name,omitempty"`
	MACAddress     string         `json:"mac_address,omitempty"`
	RegisteredAt   string         `json:"registered_at"`
	LastSeenAt     string         `json:"last_seen_at"`
	Approved       bool           `json:"approved"`
	ApprovedAt     string         `json:"approved_at,omitempty"`
	Blocked        bool           `json:"blocked"`
	BlockedAt      string         `json:"blocked_at,omitempty"`
	TokenIssuedAt  string         `json:"token_issued_at,omitempty"`
	TokenExpiresAt string         `json:"token_expires_at,omitempty"`
	Token          string         `json:"token,omitempty"`
	ReadOnly       bool           `json:"read_only"`
	ReadOnlyAt     string         `json:"read_only_at,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}

func (s *Store) registryPath() string {
	return filepath.Join(s.dataDir, "agent_registry.json")
}

func cloneMeta(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Store) LoadRegistry() error {
	b, err := os.ReadFile(s.registryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var disk map[string]agentRegistryDiskEntry
	if err := json.Unmarshal(b, &disk); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentRegistry = make(map[string]*AgentRegistryEntry, len(disk))
	for clientID, item := range disk {
		clientID = strings.TrimSpace(clientID)
		if clientID == "" {
			continue
		}
		s.agentRegistry[clientID] = &AgentRegistryEntry{
			ClientID:       clientID,
			DisplayName:    strings.TrimSpace(item.DisplayName),
			MACAddress:     normalizeMACAddress(item.MACAddress),
			RegisteredAt:   strings.TrimSpace(item.RegisteredAt),
			LastSeenAt:     strings.TrimSpace(item.LastSeenAt),
			Approved:       item.Approved,
			ApprovedAt:     strings.TrimSpace(item.ApprovedAt),
			Blocked:        item.Blocked,
			BlockedAt:      strings.TrimSpace(item.BlockedAt),
			TokenIssued:    strings.TrimSpace(item.Token) != "",
			TokenIssuedAt:  strings.TrimSpace(item.TokenIssuedAt),
			TokenExpiresAt: strings.TrimSpace(item.TokenExpiresAt),
			Token:          strings.TrimSpace(item.Token),
			ReadOnly:       item.ReadOnly,
			ReadOnlyAt:     strings.TrimSpace(item.ReadOnlyAt),
			Meta:           cloneMeta(item.Meta),
		}
	}
	return nil
}

func (s *Store) SaveRegistry() error {
	s.mu.RLock()
	disk := make(map[string]agentRegistryDiskEntry, len(s.agentRegistry))
	for clientID, item := range s.agentRegistry {
		if item == nil {
			continue
		}
		disk[clientID] = agentRegistryDiskEntry{
			DisplayName:    item.DisplayName,
			MACAddress:     item.MACAddress,
			RegisteredAt:   item.RegisteredAt,
			LastSeenAt:     item.LastSeenAt,
			Approved:       item.Approved,
			ApprovedAt:     item.ApprovedAt,
			Blocked:        item.Blocked,
			BlockedAt:      item.BlockedAt,
			TokenIssuedAt:  item.TokenIssuedAt,
			TokenExpiresAt: item.TokenExpiresAt,
			Token:          item.Token,
			ReadOnly:       item.ReadOnly,
			ReadOnlyAt:     item.ReadOnlyAt,
			Meta:           cloneMeta(item.Meta),
		}
	}
	s.mu.RUnlock()

	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	if s.pg != nil {
		s.mu.RLock()
		for _, item := range s.agentRegistry {
			if item != nil {
				_ = s.pg.SaveAgentRegistryEntry(item)
			}
		}
		s.mu.RUnlock()
	}
	if s.dataDir == "" {
		return nil
	}
	return os.WriteFile(s.registryPath(), b, 0644)
}

func (s *Store) UpsertAgentRegistry(in AgentRegistryUpsert) (AgentRegistryEntry, error) {
	clientID := strings.TrimSpace(in.ClientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}

	now := in.LastSeenAt
	if now.IsZero() {
		now = time.Now()
	}
	nowText := now.Format(time.RFC3339Nano)
	macAddress := normalizeMACAddress(in.MACAddress)

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		entry = &AgentRegistryEntry{
			ClientID:     clientID,
			RegisteredAt: nowText,
			LastSeenAt:   nowText,
			DisplayName:  strings.TrimSpace(in.DisplayName),
			MACAddress:   macAddress,
			Blocked:      false,
			TokenIssued:  false,
			Meta:         cloneMeta(in.Meta),
		}
		s.agentRegistry[clientID] = entry
	} else {
		entry.LastSeenAt = nowText
		if strings.TrimSpace(in.DisplayName) != "" {
			entry.DisplayName = strings.TrimSpace(in.DisplayName)
		}
		if macAddress != "" {
			entry.MACAddress = macAddress
		}
		if len(in.Meta) > 0 {
			if entry.Meta == nil {
				entry.Meta = map[string]any{}
			}
			for k, v := range in.Meta {
				entry.Meta[k] = v
			}
		}
	}
	out := *entry
	if entry.Meta != nil {
		out.Meta = cloneMeta(entry.Meta)
	}
	s.mu.Unlock()

	return out, s.SaveRegistry()
}

func (s *Store) SetAgentIssuedToken(clientID, token string, issuedAt, expiresAt time.Time) (AgentRegistryEntry, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		entry = &AgentRegistryEntry{
			ClientID:     clientID,
			RegisteredAt: time.Now().Format(time.RFC3339Nano),
		}
		s.agentRegistry[clientID] = entry
	}
	entry.LastSeenAt = time.Now().Format(time.RFC3339Nano)
	entry.Approved = strings.TrimSpace(token) != ""
	entry.ApprovedAt = issuedAt.Format(time.RFC3339Nano)
	entry.TokenIssued = strings.TrimSpace(token) != ""
	entry.TokenIssuedAt = issuedAt.Format(time.RFC3339Nano)
	entry.TokenExpiresAt = expiresAt.Format(time.RFC3339Nano)
	entry.Token = strings.TrimSpace(token)
	out := *entry
	if entry.Meta != nil {
		out.Meta = cloneMeta(entry.Meta)
	}
	s.mu.Unlock()

	return out, s.SaveRegistry()
}

func (s *Store) SetAgentApproval(clientID string, approved bool, at time.Time) (AgentRegistryEntry, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		entry = &AgentRegistryEntry{
			ClientID:     clientID,
			RegisteredAt: at.Format(time.RFC3339Nano),
		}
		s.agentRegistry[clientID] = entry
	}
	entry.LastSeenAt = at.Format(time.RFC3339Nano)
	entry.Approved = approved
	if approved {
		entry.ApprovedAt = at.Format(time.RFC3339Nano)
	} else {
		entry.ApprovedAt = ""
		entry.TokenIssued = false
		entry.TokenIssuedAt = ""
		entry.TokenExpiresAt = ""
		entry.Token = ""
	}
	out := *entry
	if entry.Meta != nil {
		out.Meta = cloneMeta(entry.Meta)
	}
	s.mu.Unlock()

	if !approved {
		_ = s.DeleteAuthTokensForClientKind(clientID, "dev-short")
	}
	return out, s.SaveRegistry()
}

func (s *Store) SetAgentBlocked(clientID string, blocked bool, at time.Time) (AgentRegistryEntry, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		entry = &AgentRegistryEntry{
			ClientID:     clientID,
			RegisteredAt: at.Format(time.RFC3339Nano),
		}
		s.agentRegistry[clientID] = entry
	}
	entry.LastSeenAt = at.Format(time.RFC3339Nano)
	entry.Blocked = blocked
	if blocked {
		entry.BlockedAt = at.Format(time.RFC3339Nano)
	} else {
		entry.BlockedAt = ""
	}
	out := *entry
	if entry.Meta != nil {
		out.Meta = cloneMeta(entry.Meta)
	}
	s.mu.Unlock()

	return out, s.SaveRegistry()
}

const InactiveThresholdForReadOnly = 30 * 24 * time.Hour

func (s *Store) SetAgentReadOnly(clientID string, readOnly bool, at time.Time) (AgentRegistryEntry, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		s.mu.Unlock()
		return AgentRegistryEntry{}, fmt.Errorf("agent not found")
	}
	entry.ReadOnly = readOnly
	if readOnly {
		entry.ReadOnlyAt = at.Format(time.RFC3339Nano)
	} else {
		entry.ReadOnlyAt = ""
		entry.LastSeenAt = at.Format(time.RFC3339Nano)
	}
	out := *entry
	if entry.Meta != nil {
		out.Meta = cloneMeta(entry.Meta)
	}
	s.mu.Unlock()

	return out, s.SaveRegistry()
}

func (s *Store) IsAgentReadOnly(clientID string) bool {
	if s == nil {
		return false
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}

	s.mu.RLock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		s.mu.RUnlock()
		return false
	}
	if entry.ReadOnly {
		s.mu.RUnlock()
		return true
	}
	if entry.LastSeenAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, entry.LastSeenAt); err == nil && !t.IsZero() {
			if time.Since(t) >= InactiveThresholdForReadOnly {
				s.mu.RUnlock()
				go func() {
					_, _ = s.SetAgentReadOnly(clientID, true, time.Now())
				}()
				return true
			}
		}
	}
	s.mu.RUnlock()
	return false
}

func (s *Store) ListAgentRegistry() []AgentRegistryEntry {
	s.mu.Lock()
	now := time.Now()
	needSave := false
	out := make([]AgentRegistryEntry, 0, len(s.agentRegistry))
	for _, item := range s.agentRegistry {
		if item == nil {
			continue
		}
		if !item.ReadOnly && item.LastSeenAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, item.LastSeenAt); err == nil && !t.IsZero() {
				if now.Sub(t) >= InactiveThresholdForReadOnly {
					item.ReadOnly = true
					item.ReadOnlyAt = now.Format(time.RFC3339Nano)
					needSave = true
				}
			}
		}
		cp := *item
		if item.Meta != nil {
			cp.Meta = cloneMeta(item.Meta)
		}
		out = append(out, cp)
	}
	if needSave {
		_ = s.saveRegistryLocked()
	}
	s.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ClientID) < strings.ToLower(out[j].ClientID)
	})
	return out
}

func (s *Store) saveRegistryLocked() error {
	disk := make(map[string]agentRegistryDiskEntry, len(s.agentRegistry))
	for clientID, item := range s.agentRegistry {
		if item == nil {
			continue
		}
		disk[clientID] = agentRegistryDiskEntry{
			DisplayName:    item.DisplayName,
			MACAddress:     item.MACAddress,
			RegisteredAt:   item.RegisteredAt,
			LastSeenAt:     item.LastSeenAt,
			Approved:       item.Approved,
			ApprovedAt:     item.ApprovedAt,
			Blocked:        item.Blocked,
			BlockedAt:      item.BlockedAt,
			TokenIssuedAt:  item.TokenIssuedAt,
			TokenExpiresAt: item.TokenExpiresAt,
			Token:          item.Token,
			ReadOnly:       item.ReadOnly,
			ReadOnlyAt:     item.ReadOnlyAt,
			Meta:           cloneMeta(item.Meta),
		}
	}

	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	if s.pg != nil {
		for _, item := range s.agentRegistry {
			if item != nil {
				_ = s.pg.SaveAgentRegistryEntry(item)
			}
		}
	}
	if s.dataDir == "" {
		return nil
	}
	return os.WriteFile(s.registryPath(), b, 0644)
}

func (s *Store) FindAgentRegistryByMAC(macAddress string) (AgentRegistryEntry, bool) {
	macAddress = normalizeMACAddress(macAddress)
	if macAddress == "" {
		return AgentRegistryEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.agentRegistry {
		if item == nil || normalizeMACAddress(item.MACAddress) != macAddress {
			continue
		}
		out := *item
		if item.Meta != nil {
			out.Meta = cloneMeta(item.Meta)
		}
		return out, true
	}
	return AgentRegistryEntry{}, false
}

func (s *Store) GetAgentRegistry(clientID string) (AgentRegistryEntry, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AgentRegistryEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.agentRegistry[clientID]
	if !ok || item == nil {
		return AgentRegistryEntry{}, false
	}
	cp := *item
	if item.Meta != nil {
		cp.Meta = cloneMeta(item.Meta)
	}
	return cp, true
}

func (s *Store) FindTrustedAgentByMACAndIP(macAddress, sourceIP string) (AgentRegistryEntry, bool) {
	macAddress = normalizeMACAddress(macAddress)
	sourceIP = strings.TrimSpace(sourceIP)
	if macAddress == "" || sourceIP == "" {
		return AgentRegistryEntry{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.agentRegistry {
		if entry == nil {
			continue
		}
		if entry.Blocked {
			continue
		}
		if normalizeMACAddress(entry.MACAddress) != macAddress {
			continue
		}
		if entry.Meta == nil {
			continue
		}
		loginIP, _ := entry.Meta["dev_login_ip"].(string)
		if strings.TrimSpace(loginIP) == sourceIP {
			out := *entry
			if entry.Meta != nil {
				out.Meta = cloneMeta(entry.Meta)
			}
			return out, true
		}
	}
	return AgentRegistryEntry{}, false
}

func (s *Store) IsIssuedTokenValid(clientID, token string) bool {
	clientID = strings.TrimSpace(clientID)
	token = strings.TrimSpace(token)
	if clientID == "" || token == "" {
		return false
	}

	s.mu.RLock()
	item, ok := s.agentRegistry[clientID]
	if !ok || item == nil {
		s.mu.RUnlock()
		return false
	}
	if !item.TokenIssued || strings.TrimSpace(item.Token) != token {
		s.mu.RUnlock()
		return false
	}
	expiresAt := strings.TrimSpace(item.TokenExpiresAt)
	s.mu.RUnlock()

	if expiresAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, expiresAt); err == nil && ts.Before(time.Now()) {
			return false
		}
	}
	return true
}

func (s *Store) DeleteAgentRegistry(clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ErrMissingClientID
	}

	s.mu.Lock()
	delete(s.agentRegistry, clientID)
	delete(s.roomACLs, clientID)
	for token, item := range s.authTokens {
		if item == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.ClientID), clientID) {
			delete(s.authTokens, token)
		}
	}
	s.mu.Unlock()

	if s.pg != nil {
		_ = s.pg.DeleteAgentRegistry(clientID)
		_ = s.pg.DeleteRoomACL(clientID)
		_ = s.pg.DeleteAuthTokensForClient(clientID)
	}

	if err := s.SaveRegistry(); err != nil {
		return err
	}
	if err := s.SaveACLs(); err != nil {
		return err
	}
	return s.SaveAuthTokens()
}
