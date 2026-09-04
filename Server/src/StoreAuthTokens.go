package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AuthTokenRecord struct {
	Token      string `json:"token"`
	ClientID   string `json:"client_id"`
	Kind       string `json:"kind"`
	MACAddress string `json:"mac_address,omitempty"`
	SourceIP   string `json:"source_ip,omitempty"`
	IssuedAt   string `json:"issued_at"`
	ExpiresAt  string `json:"expires_at"`
}

func (s *Store) authTokensPath() string {
	return filepath.Join(s.dataDir, "auth_tokens.json")
}

func (s *Store) LoadAuthTokens() error {
	b, err := os.ReadFile(s.authTokensPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var disk map[string]AuthTokenRecord
	if err := json.Unmarshal(b, &disk); err != nil {
		return err
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authTokens = make(map[string]*AuthTokenRecord, len(disk))
	for token, item := range disk {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if isAuthTokenExpired(item.ExpiresAt, now) {
			continue
		}
		cp := item
		cp.Token = token
		cp.ClientID = strings.TrimSpace(cp.ClientID)
		cp.Kind = strings.TrimSpace(cp.Kind)
		cp.MACAddress = normalizeMACAddress(cp.MACAddress)
		cp.SourceIP = strings.TrimSpace(cp.SourceIP)
		cp.IssuedAt = strings.TrimSpace(cp.IssuedAt)
		cp.ExpiresAt = strings.TrimSpace(cp.ExpiresAt)
		s.authTokens[token] = &cp
	}
	return nil
}

func (s *Store) SaveAuthTokens() error {
	s.mu.RLock()
	disk := make(map[string]AuthTokenRecord, len(s.authTokens))
	for token, item := range s.authTokens {
		if item == nil {
			continue
		}
		cp := *item
		disk[token] = cp
	}
	s.mu.RUnlock()

	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	if s.pg != nil {
		s.mu.RLock()
		for _, item := range s.authTokens {
			if item != nil {
				_ = s.pg.SaveAuthToken(item)
			}
		}
		s.mu.RUnlock()
	}
	if s.dataDir == "" {
		return nil
	}
	return writePrivateFile(s.authTokensPath(), b)
}

func isAuthTokenExpired(expiresAt string, now time.Time) bool {
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return true
	}
	return ts.Before(now)
}

func (s *Store) UpsertAuthToken(record AuthTokenRecord, replaceUnique bool) (AuthTokenRecord, error) {
	record.Token = strings.TrimSpace(record.Token)
	record.ClientID = strings.TrimSpace(record.ClientID)
	record.Kind = strings.TrimSpace(record.Kind)
	record.MACAddress = normalizeMACAddress(record.MACAddress)
	record.SourceIP = strings.TrimSpace(record.SourceIP)
	record.IssuedAt = strings.TrimSpace(record.IssuedAt)
	record.ExpiresAt = strings.TrimSpace(record.ExpiresAt)
	if record.Token == "" || record.ClientID == "" || record.Kind == "" {
		return AuthTokenRecord{}, ErrMissingClientID
	}
	if record.IssuedAt == "" {
		record.IssuedAt = time.Now().Format(time.RFC3339Nano)
	}

	s.mu.Lock()
	if s.authTokens == nil {
		s.authTokens = map[string]*AuthTokenRecord{}
	}
	if replaceUnique {
		for token, item := range s.authTokens {
			if item == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(item.ClientID), record.ClientID) && strings.EqualFold(strings.TrimSpace(item.Kind), record.Kind) {
				delete(s.authTokens, token)
			}
		}
	}
	cp := record
	s.authTokens[record.Token] = &cp
	s.mu.Unlock()

	if replaceUnique && s.pg != nil {
		_ = s.pg.DeleteAuthTokensForClientKind(record.ClientID, record.Kind)
	}

	return record, s.SaveAuthTokens()
}

func (s *Store) GetAuthTokenRecord(token string) (AuthTokenRecord, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AuthTokenRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.authTokens[token]
	if !ok || item == nil {
		return AuthTokenRecord{}, false
	}
	return *item, true
}

func (s *Store) FindAuthTokenByClientKind(clientID, kind string) (AuthTokenRecord, bool) {
	clientID = strings.TrimSpace(clientID)
	kind = strings.TrimSpace(kind)
	if clientID == "" || kind == "" {
		return AuthTokenRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.authTokens {
		if item == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.ClientID), clientID) && strings.EqualFold(strings.TrimSpace(item.Kind), kind) {
			return *item, true
		}
	}
	return AuthTokenRecord{}, false
}

func isMatchingSourceIP(recorded, current string) bool {
	recorded = strings.TrimSpace(recorded)
	current = strings.TrimSpace(current)
	if recorded == "" || current == "" || recorded == current {
		return true
	}
	recIP := net.ParseIP(recorded)
	curIP := net.ParseIP(current)
	if recIP != nil && curIP != nil && recIP.IsLoopback() && curIP.IsLoopback() {
		return true
	}
	return false
}

func (s *Store) AuthorizeAuthToken(token, sourceIP string) (AuthTokenRecord, bool) {
	token = strings.TrimSpace(token)
	if token == "" || s == nil {
		return AuthTokenRecord{}, false
	}

	var (
		out     AuthTokenRecord
		changed bool
	)

	s.mu.Lock()
	item, ok := s.authTokens[token]
	if !ok || item == nil {
		// 1. Fallback: Search agentRegistry for this exact token
		for _, entry := range s.agentRegistry {
			if entry != nil && entry.Approved && !entry.Blocked && strings.TrimSpace(entry.Token) == token {
				issuedAt := strings.TrimSpace(entry.TokenIssuedAt)
				expiresAt := strings.TrimSpace(entry.TokenExpiresAt)
				if issuedAt == "" {
					issuedAt = time.Now().Format(time.RFC3339Nano)
				}
				if expiresAt == "" {
					expiresAt = time.Now().Add(time.Duration(devShortTokenTTLSec) * time.Second).Format(time.RFC3339Nano)
				}
				kind := "dev-short"
				if entry.IsAdmin || strings.EqualFold(entry.ClientID, "root") {
					kind = "system"
				}
				item = &AuthTokenRecord{
					Token:      token,
					ClientID:   entry.ClientID,
					Kind:       kind,
					MACAddress: entry.MACAddress,
					SourceIP:   sourceIP,
					IssuedAt:   issuedAt,
					ExpiresAt:  expiresAt,
				}
				s.authTokens[token] = item
				ok = true
				changed = true
				break
			}
		}

		if !ok || item == nil {
			s.mu.Unlock()
			return AuthTokenRecord{}, false
		}
	}

	// Expired credentials are never renewed implicitly. Renewal must issue a
	// fresh credential through an authenticated/admin-controlled flow.
	if isAuthTokenExpired(item.ExpiresAt, time.Now()) {
		delete(s.authTokens, token)
		s.mu.Unlock()
		_ = s.SaveAuthTokens()
		return AuthTokenRecord{}, false
	}

	// 4. Verify agent status in registry
	entry, hasEntry := s.agentRegistry[item.ClientID]
	if hasEntry && entry != nil {
		if entry.Blocked || !entry.Approved {
			s.mu.Unlock()
			return AuthTokenRecord{}, false
		}
		if (strings.EqualFold(item.Kind, "dev-short") || strings.EqualFold(item.Kind, "agent") || strings.EqualFold(item.Kind, "system")) &&
			(strings.TrimSpace(entry.Token) == "" || strings.TrimSpace(entry.Token) != token) {
			s.mu.Unlock()
			return AuthTokenRecord{}, false
		}
		entry.LastSeenAt = time.Now().Format(time.RFC3339)
	}

	// 5. Source IP matching & Recording
	// If the agent exists in agentRegistry and is approved, the bearer token proves authorization.
	// We record the current source IP rather than rejecting the agent due to dynamic IP or proxy.
	if hasEntry && entry != nil && entry.Approved && !entry.Blocked {
		if sourceIP != "" && item.SourceIP != sourceIP {
			item.SourceIP = sourceIP
			changed = true
		}
	} else if strings.EqualFold(item.Kind, "session-human") {
		// Human sessions allow IP change (e.g. mobile or reverse proxy)
		if sourceIP != "" && item.SourceIP != sourceIP {
			item.SourceIP = sourceIP
			changed = true
		}
	} else {
		// For standalone unregistered tokens without an agentRegistry entry, maintain IP binding
		if item.SourceIP != "" && sourceIP != "" && !isMatchingSourceIP(item.SourceIP, sourceIP) {
			s.mu.Unlock()
			return AuthTokenRecord{}, false
		}
		if item.SourceIP == "" && sourceIP != "" {
			item.SourceIP = sourceIP
			changed = true
		}
	}

	out = *item
	s.mu.Unlock()

	if changed {
		_ = s.SaveAuthTokens()
	}
	return out, true
}

func (s *Store) EnsureAgentAuthTokenRecord(clientID, token, macAddress, sourceIP string) error {
	token = strings.TrimSpace(token)
	clientID = strings.TrimSpace(clientID)
	if token == "" || clientID == "" || s == nil {
		return nil
	}
	now := time.Now()
	exp := now.Add(time.Duration(devShortTokenTTLSec) * time.Second)
	rec := AuthTokenRecord{
		Token:      token,
		ClientID:   clientID,
		Kind:       "dev-short",
		MACAddress: normalizeMACAddress(macAddress),
		SourceIP:   strings.TrimSpace(sourceIP),
		IssuedAt:   now.Format(time.RFC3339Nano),
		ExpiresAt:  exp.Format(time.RFC3339Nano),
	}
	_, err := s.UpsertAuthToken(rec, true)
	return err
}

func (s *Store) DeleteAuthTokensForClientKind(clientID, kind string) error {
	clientID = strings.TrimSpace(clientID)
	kind = strings.TrimSpace(kind)
	if clientID == "" || kind == "" {
		return nil
	}
	s.mu.Lock()
	for token, item := range s.authTokens {
		if item == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.ClientID), clientID) && strings.EqualFold(strings.TrimSpace(item.Kind), kind) {
			delete(s.authTokens, token)
		}
	}
	s.mu.Unlock()
	return s.SaveAuthTokens()
}

func (s *Store) ListAuthTokenRecords() []AuthTokenRecord {
	s.mu.RLock()
	out := make([]AuthTokenRecord, 0, len(s.authTokens))
	for _, item := range s.authTokens {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClientID == out[j].ClientID {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ClientID < out[j].ClientID
	})
	return out
}
