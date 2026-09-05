package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const webViewPrefix = "stv1."
const webViewCookie = "smalltalk_view_request"
const webViewTTL = 24 * time.Hour

type webViewRequest struct {
	ID          string    `json:"id"`
	BrowserHash string    `json:"browser_hash"`
	SourceHash  string    `json:"source_hash"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	ClientID    string    `json:"client_id,omitempty"`
	ParentHash  string    `json:"parent_hash,omitempty"`
	Activated   bool      `json:"activated"`
}

func viewHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func viewRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func validViewID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 32 && len(id) == 64
}
func parseViewToken(token string) (string, string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "stv1" || !validViewID(parts[1]) || !validViewID(parts[2]) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// 呼叫端持有 s.mu；只保存雜湊，不保存瀏覽器密鑰或 Agent TOKEN。
func (s *Store) readViewRequestsLocked() (map[string]webViewRequest, error) {
	records := map[string]webViewRequest{}
	if s.pg != nil {
		rows, err := s.pg.db.Query(`SELECT payload FROM browser_view_requests WHERE expires_at > NOW()`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var record webViewRequest
			if err := rows.Scan(&raw); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &record); err != nil {
				return nil, err
			}
			records[record.ID] = record
		}
		return records, rows.Err()
	}
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "browser_view_requests.json"))
	if os.IsNotExist(err) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	for id, record := range records {
		if !time.Now().Before(record.ExpiresAt) {
			delete(records, id)
		}
	}
	return records, nil
}
func (s *Store) readViewRequestLocked(id string) (map[string]webViewRequest, error) {
	if s.pg == nil {
		return s.readViewRequestsLocked()
	}
	var raw []byte
	err := s.pg.db.QueryRow(`SELECT payload FROM browser_view_requests WHERE id=$1 AND expires_at > NOW()`, id).Scan(&raw)
	records := map[string]webViewRequest{}
	if err == sql.ErrNoRows {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	var record webViewRequest
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	records[id] = record
	return records, nil
}
func (s *Store) saveViewRequestLocked(records map[string]webViewRequest, record webViewRequest) error {
	if s.pg != nil {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		_, err = s.pg.db.Exec(`INSERT INTO browser_view_requests(id,expires_at,payload) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET expires_at=EXCLUDED.expires_at,payload=EXCLUDED.payload`, record.ID, record.ExpiresAt, string(raw))
		return err
	}
	records[record.ID] = record
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return writeViewSnapshot(filepath.Join(s.dataDir, "browser_view_requests.json"), raw)
}
func (s *Store) viewParentActiveLocked(record webViewRequest) bool {
	entry := s.agentRegistry[record.ClientID]
	if entry == nil || !entry.Approved || entry.Blocked || entry.Token == "" || viewHash(entry.Token) != record.ParentHash {
		return false
	}
	parent := s.authTokens[entry.Token]
	if parent == nil || isAuthTokenExpired(parent.ExpiresAt, time.Now()) {
		return false
	}
	return true
}
func (s *Store) createViewRequest(source string) (webViewRequest, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readViewRequestsLocked()
	if err != nil {
		return webViewRequest{}, "", err
	}
	count := 0
	now := time.Now()
	sourceHash := viewHash(source)
	for _, record := range records {
		if record.SourceHash == sourceHash && now.Sub(record.CreatedAt) < time.Hour {
			count++
		}
	}
	if count >= 10 || len(records) >= 2000 {
		return webViewRequest{}, "", fmt.Errorf("授權請求過多，請稍後再試")
	}
	id, err := viewRandom()
	if err != nil {
		return webViewRequest{}, "", err
	}
	secret, err := viewRandom()
	if err != nil {
		return webViewRequest{}, "", err
	}
	record := webViewRequest{ID: id, BrowserHash: viewHash(secret), SourceHash: sourceHash, CreatedAt: now, ExpiresAt: now.Add(webViewTTL)}
	if s.pg != nil {
		if _, err := s.pg.db.Exec(`DELETE FROM browser_view_requests WHERE expires_at <= NOW()`); err != nil {
			return webViewRequest{}, "", err
		}
	}
	err = s.saveViewRequestLocked(records, record)
	return record, webViewPrefix + id + "." + secret, err
}
func (s *Store) approveViewRequest(id string, p *requestAuthContext) error {
	if !validViewID(id) || p == nil || p.ReadOnly || (p.TokenKind != "dev-short" && p.TokenKind != "agent" && p.TokenKind != "system") {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readViewRequestLocked(id)
	if err != nil {
		return err
	}
	record, ok := records[id]
	if !ok || !time.Now().Before(record.ExpiresAt) {
		return fmt.Errorf("授權連結已失效，請人類重新產生")
	}
	entry := s.agentRegistry[p.ClientID]
	if entry == nil || !entry.Approved || entry.Blocked || entry.Token == "" || viewHash(entry.Token) != p.CredentialHash {
		return ErrForbidden
	}
	if record.ClientID != "" {
		if record.ClientID == p.ClientID && record.ParentHash == p.CredentialHash {
			return nil
		}
		return fmt.Errorf("此請求已由其他帳號核准")
	}
	record.ClientID = p.ClientID
	record.ParentHash = p.CredentialHash
	if !s.viewParentActiveLocked(record) {
		return ErrForbidden
	}
	return s.saveViewRequestLocked(records, record)
}
func (s *Store) pollViewRequest(token string) (webViewRequest, error) {
	id, secret, ok := parseViewToken(token)
	if !ok {
		return webViewRequest{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readViewRequestLocked(id)
	if err != nil {
		return webViewRequest{}, err
	}
	record, ok := records[id]
	if !ok || record.BrowserHash != viewHash(secret) || !time.Now().Before(record.ExpiresAt) {
		return webViewRequest{}, fmt.Errorf("授權請求已失效，請重新產生連結")
	}
	if record.ClientID == "" {
		return record, nil
	}
	if !s.viewParentActiveLocked(record) {
		return webViewRequest{}, fmt.Errorf("帳號授權已失效，請重新請求")
	}
	if !record.Activated {
		record.Activated = true
		record.ExpiresAt = time.Now().Add(webViewTTL)
		if err := s.saveViewRequestLocked(records, record); err != nil {
			return webViewRequest{}, err
		}
	}
	return record, nil
}
func (s *Store) authorizeViewToken(token string) (*requestAuthContext, bool) {
	id, secret, ok := parseViewToken(token)
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readViewRequestLocked(id)
	if err != nil {
		return nil, false
	}
	record, ok := records[id]
	if !ok || !record.Activated || record.BrowserHash != viewHash(secret) || !time.Now().Before(record.ExpiresAt) || !s.viewParentActiveLocked(record) {
		return nil, false
	}
	return &requestAuthContext{Kind: "smalltalk-view", PrincipalType: "human", TokenKind: "browser-view", ClientID: record.ClientID, ReadOnly: true, AuthExpiresAt: record.ExpiresAt.Format(time.RFC3339)}, true
}
func (s *Store) revokeViewToken(token string) error {
	id, secret, ok := parseViewToken(token)
	if !ok {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readViewRequestLocked(id)
	if err != nil {
		return err
	}
	record, ok := records[id]
	if !ok {
		return nil
	}
	if record.BrowserHash != viewHash(secret) {
		return ErrForbidden
	}
	record.ExpiresAt = time.Now().Add(-time.Second)
	return s.saveViewRequestLocked(records, record)
}
func (h *HttpAPI_auth) handleViewLogin(w http.ResponseWriter, r *http.Request, action string) []byte {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// 建立、輪詢及換發 cookie 皆要求同源 POST，防止跨站植入登入。
	if r.Method != http.MethodPost || !originMatchesRequestHost(r.Header.Get("Origin"), r, h.Store) {
		w.WriteHeader(http.StatusForbidden)
		return mustJSON(ErrorResponse{Error: "請從本站帳號設定操作"})
	}
	if h.Store == nil {
		return mustJSON(ErrorResponse{Error: "service unavailable"})
	}
	if action == "request" {
		record, token, err := h.Store.createViewRequest(sourceIPOfWithStore(r, h.Store))
		if err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		http.SetCookie(w, &http.Cookie{Name: webViewCookie, Value: token, Path: "/auth/view", HttpOnly: true, Secure: requestUsesHTTPS(r, h.Store), SameSite: http.SameSiteStrictMode, MaxAge: 86400})
		return mustJSON(map[string]any{"ok": true, "request_id": record.ID, "expires_at": record.ExpiresAt.Format(time.RFC3339)})
	}
	if action == "poll" || action == "resume" {
		cookie, err := r.Cookie(webViewCookie)
		if err != nil {
			if action == "resume" {
				return mustJSON(map[string]any{"ok": true, "status": "idle"})
			}
			return mustJSON(ErrorResponse{Error: "請先產生授權連結"})
		}
		record, err := h.Store.pollViewRequest(cookie.Value)
		if err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		if !record.Activated {
			return mustJSON(map[string]any{"ok": true, "status": "pending", "request_id": record.ID, "expires_at": record.ExpiresAt.Format(time.RFC3339)})
		}
		remaining := int(time.Until(record.ExpiresAt).Seconds())
		if remaining < 1 {
			return mustJSON(ErrorResponse{Error: "登入已到期"})
		}
		http.SetCookie(w, &http.Cookie{Name: "smalltalk_auth_token", Value: cookie.Value, Path: "/", HttpOnly: true, Secure: requestUsesHTTPS(r, h.Store), SameSite: http.SameSiteLaxMode, MaxAge: remaining})
		return mustJSON(map[string]any{"ok": true, "status": "approved", "read_only": true, "expires_at": record.ExpiresAt.Format(time.RFC3339)})
	}
	return mustJSON(ErrorResponse{Error: "not found"})
}

// 以同目錄原子替換保存，寫入失敗不破壞其他有效 session。
func writeViewSnapshot(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".browser-view-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
