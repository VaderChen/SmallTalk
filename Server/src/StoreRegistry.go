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
	IsAdmin        bool           `json:"is_admin"`
	IsAdminAt      string         `json:"is_admin_at,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`

	postgresRoleLoaded bool // 區分舊資料未遷移與已明確撤銷管理角色。
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
	IsAdmin        bool           `json:"is_admin"`
	IsAdminAt      string         `json:"is_admin_at,omitempty"`
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
			IsAdmin:        item.IsAdmin,
			IsAdminAt:      strings.TrimSpace(item.IsAdminAt),
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
			IsAdmin:        item.IsAdmin,
			IsAdminAt:      item.IsAdminAt,
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
				if err := s.pg.SaveAgentRegistryEntry(item); err != nil {
					s.mu.RUnlock()
					return err
				}
			}
		}
		s.mu.RUnlock()
	}
	if s.dataDir == "" {
		return nil
	}
	return writePrivateFile(s.registryPath(), b)
}

func (s *Store) UpsertAgentRegistry(in AgentRegistryUpsert) (AgentRegistryEntry, error) {
	clientID := strings.TrimSpace(in.ClientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}

	newDisplayName := strings.TrimSpace(in.DisplayName)

	now := in.LastSeenAt
	if now.IsZero() {
		now = time.Now()
	}
	nowText := now.Format(time.RFC3339Nano)
	macAddress := normalizeMACAddress(in.MACAddress)

	s.mu.Lock()

	// Check for duplicate display_name across all other agents (blocks renaming or registering with taken names)
	if newDisplayName != "" {
		for otherID, otherEntry := range s.agentRegistry {
			if otherEntry == nil || otherID == clientID {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(otherEntry.DisplayName), newDisplayName) {
				s.mu.Unlock()
				return AgentRegistryEntry{}, fmt.Errorf("【名稱重複衝突】顯示名稱 '%s' 已經被其他帳號 (%s) 使用，無法使用或更改為此名稱", newDisplayName, otherID)
			}
		}
	}

	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		entry = &AgentRegistryEntry{
			ClientID:     clientID,
			RegisteredAt: nowText,
			LastSeenAt:   nowText,
			DisplayName:  newDisplayName,
			MACAddress:   macAddress,
			Blocked:      false,
			TokenIssued:  false,
			Meta:         cloneMeta(in.Meta),
		}
		s.agentRegistry[clientID] = entry
	} else {
		entry.LastSeenAt = nowText
		if newDisplayName != "" {
			entry.DisplayName = newDisplayName
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
		if err := s.DeleteAuthTokensForClientKind(clientID, "dev-short"); err != nil {
			return AgentRegistryEntry{}, err
		}
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

func (s *Store) SetAgentAdmin(clientID string, isAdmin bool) (AgentRegistryEntry, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AgentRegistryEntry{}, ErrMissingClientID
	}

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		s.mu.Unlock()
		return AgentRegistryEntry{}, fmt.Errorf("agent not found")
	}
	entry.IsAdmin = isAdmin
	if isAdmin {
		entry.IsAdminAt = time.Now().Format(time.RFC3339Nano)
	} else {
		entry.IsAdminAt = ""
	}
	out := *entry
	if entry.Meta != nil {
		out.Meta = cloneMeta(entry.Meta)
	}
	s.mu.Unlock()

	return out, s.SaveRegistry()
}

func (s *Store) GetAgentRole(clientID string) (bool, []string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false, nil, ErrMissingClientID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		return false, nil, fmt.Errorf("agent not found")
	}
	isAdmin := entry.IsAdmin
	displayName := strings.TrimSpace(entry.DisplayName)

	var modRooms []string
	for pid, p := range s.projects {
		if p == nil {
			continue
		}
		for rid, r := range p.Rooms {
			if r == nil {
				continue
			}
			owner := strings.TrimSpace(r.Owner)
			if owner != "" && (owner == clientID || (displayName != "" && owner == displayName)) {
				modRooms = append(modRooms, fmt.Sprintf("%s/%s", pid, rid))
			}
		}
	}
	return isAdmin, modRooms, nil
}

type boardRoleChange struct {
	projectID     string
	room          *Room
	previousOwner string
	owner         string
}

func (s *Store) SetAgentRole(clientID string, isAdmin bool, moderatorRooms []string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ErrMissingClientID
	}
	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		s.mu.Unlock()
		return fmt.Errorf("agent not found")
	}
	displayName := strings.TrimSpace(entry.DisplayName)
	targetSet := make(map[string]bool)
	for _, key := range moderatorRooms {
		targetSet[strings.TrimSpace(key)] = true
	}
	changes := []boardRoleChange{}
	for pid, p := range s.projects {
		if p == nil {
			continue
		}
		for rid, r := range p.Rooms {
			if r == nil || strings.EqualFold(rid, "announce") || strings.EqualFold(rid, "visitors") {
				continue
			}
			owner := strings.TrimSpace(r.Owner)
			isOwner := owner == clientID || (displayName != "" && owner == displayName)
			next := r.Owner
			if targetSet[pid+"/"+rid] || targetSet[rid] {
				// 舊名稱指派也正規化為不受更名影響的 client_id。
				next = clientID
			} else if isOwner {
				next = ""
			}
			if next != r.Owner {
				changes = append(changes, boardRoleChange{pid, r, r.Owner, next})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].projectID+"/"+changes[i].room.ID < changes[j].projectID+"/"+changes[j].room.ID
	})
	adminAt := ""
	if isAdmin {
		adminAt = time.Now().Format(time.RFC3339Nano)
	}
	if s.pg != nil {
		// 管理角色與全部版主指派同一交易，失敗不改記憶體、不回報假成功。
		if err := s.pg.saveAgentRole(clientID, isAdmin, adminAt, changes); err != nil {
			s.mu.Unlock()
			return err
		}
		entry.IsAdmin, entry.IsAdminAt, entry.postgresRoleLoaded = isAdmin, adminAt, true
		for _, change := range changes {
			change.room.Owner = change.owner
		}
		s.mu.Unlock()
		return nil
	}
	// 本地檔案依序保存；失敗回報且回復已修改的看板。
	for i, change := range changes {
		change.room.Owner = change.owner
		if err := s.saveRoomMetaLocked(change.projectID, change.room); err != nil {
			change.room.Owner = change.previousOwner
			for j := i - 1; j >= 0; j-- {
				previous := changes[j]
				previous.room.Owner = previous.previousOwner
				if restoreErr := s.saveRoomMetaLocked(previous.projectID, previous.room); restoreErr != nil {
					err = fmt.Errorf("%w; 回復版主設定: %v", err, restoreErr)
				}
			}
			s.mu.Unlock()
			return err
		}
	}
	oldAdmin, oldAt := entry.IsAdmin, entry.IsAdminAt
	entry.IsAdmin, entry.IsAdminAt = isAdmin, adminAt
	s.mu.Unlock()
	if err := s.SaveRegistry(); err != nil {
		s.mu.Lock()
		entry.IsAdmin, entry.IsAdminAt = oldAdmin, oldAt
		for _, change := range changes {
			change.room.Owner = change.previousOwner
			if restoreErr := s.saveRoomMetaLocked(change.projectID, change.room); restoreErr != nil {
				err = fmt.Errorf("%w; 回復版主設定: %v", err, restoreErr)
			}
		}
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Store) IsAgentReadOnly(clientID string) bool {
	if s == nil {
		return false
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}

	s.mu.Lock()
	entry, ok := s.agentRegistry[clientID]
	if !ok || entry == nil {
		s.mu.Unlock()
		return false
	}
	if entry.ReadOnly {
		s.mu.Unlock()
		return true
	}
	if entry.LastSeenAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, entry.LastSeenAt); err == nil && !t.IsZero() {
			if time.Since(t) >= InactiveThresholdForReadOnly {
				entry.ReadOnly = true
				entry.ReadOnlyAt = time.Now().Format(time.RFC3339Nano)
				s.mu.Unlock()
				_ = s.SaveRegistry()
				return true
			}
		}
	}
	s.mu.Unlock()
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

func redactAgentCredential(entry AgentRegistryEntry) AgentRegistryEntry {
	entry.Token = ""
	return entry
}

func (s *Store) ListAgentRegistryRedacted() []AgentRegistryEntry {
	items := s.ListAgentRegistry()
	for i := range items {
		items[i] = redactAgentCredential(items[i])
	}
	return items
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
				if err := s.pg.SaveAgentRegistryEntry(item); err != nil {
					return err
				}
			}
		}
	}
	if s.dataDir == "" {
		return nil
	}
	return writePrivateFile(s.registryPath(), b)
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

func isSameSubnetOrLocal(ip1, ip2 string) bool {
	ip1 = strings.TrimSpace(ip1)
	ip2 = strings.TrimSpace(ip2)
	if ip1 == "" || ip2 == "" {
		return false
	}
	if ip1 == ip2 {
		return true
	}
	if (ip1 == "127.0.0.1" || ip1 == "::1" || ip1 == "localhost") &&
		(ip2 == "127.0.0.1" || ip2 == "::1" || ip2 == "localhost") {
		return true
	}
	p1 := strings.Split(ip1, ".")
	p2 := strings.Split(ip2, ".")
	if len(p1) == 4 && len(p2) == 4 {
		return p1[0] == p2[0] && p1[1] == p2[1] && p1[2] == p2[2]
	}
	return false
}

func (s *Store) FindAgentRegistryByDisplayName(displayName, sourceIP string) (AgentRegistryEntry, bool) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return AgentRegistryEntry{}, false
	}
	sourceIP = strings.TrimSpace(sourceIP)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var exactIPMatch *AgentRegistryEntry
	var fallbackMatch *AgentRegistryEntry

	for _, item := range s.agentRegistry {
		if item == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.DisplayName), displayName) {
			continue
		}

		itemIP := ""
		if item.Meta != nil {
			if ip, ok := item.Meta["source_ip"].(string); ok {
				itemIP = strings.TrimSpace(ip)
			}
			if itemIP == "" {
				if ip, ok := item.Meta["dev_login_ip"].(string); ok {
					itemIP = strings.TrimSpace(ip)
				}
			}
		}

		if sourceIP != "" && itemIP != "" && isSameSubnetOrLocal(itemIP, sourceIP) {
			if exactIPMatch == nil || (!exactIPMatch.TokenIssued && item.TokenIssued) || item.LastSeenAt > exactIPMatch.LastSeenAt {
				exactIPMatch = item
			}
		}

		if fallbackMatch == nil {
			fallbackMatch = item
		} else if (!fallbackMatch.Approved && item.Approved) || (!fallbackMatch.TokenIssued && item.TokenIssued) || item.LastSeenAt > fallbackMatch.LastSeenAt {
			fallbackMatch = item
		}
	}

	best := exactIPMatch
	if best == nil {
		best = fallbackMatch
	}

	if best != nil {
		out := *best
		if best.Meta != nil {
			out.Meta = cloneMeta(best.Meta)
		}
		return out, true
	}

	return AgentRegistryEntry{}, false
}

func (s *Store) FindAgentRegistryByExactDisplayName(displayName string) (AgentRegistryEntry, bool) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return AgentRegistryEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best *AgentRegistryEntry
	for _, item := range s.agentRegistry {
		if item == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.DisplayName), displayName) {
			if best == nil || (!best.TokenIssued && item.TokenIssued) || item.LastSeenAt > best.LastSeenAt {
				best = item
			}
		}
	}
	if best != nil {
		out := *best
		if best.Meta != nil {
			out.Meta = cloneMeta(best.Meta)
		}
		return out, true
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

	if s.pg != nil {
		if err := s.pg.DeleteAgentData(clientID); err != nil {
			return err
		}
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

	if err := s.SaveRegistry(); err != nil {
		return err
	}
	if err := s.SaveACLs(); err != nil {
		return err
	}
	return s.SaveAuthTokens()
}
