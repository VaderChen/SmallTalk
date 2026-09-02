package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsClient"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
)

type HttpAPI_auth struct {
	MarsCloudURL    string
	DefaultAccount  string
	DefaultPassword string
	DefaultProj     string
	WebEntryPath    string
	MarsClient      *MarsClient.MarsClient
	Store           *Store
}

const loginSessionTTLSec = 24 * 60 * 60
const devShortTokenTTLSec = 10 * 365 * 24 * 60 * 60

const (
	devLoginStatusSuccess      = "success"
	devLoginStatusUnregistered = "unregistered"
	devLoginStatusPending      = "pending"
	devLoginStatusBlocked      = "blocked"
	devLoginStatusError        = "error"
)

func (h *HttpAPI_auth) Process(w http.ResponseWriter, r *http.Request, jwt *MarsJSON.JSONObject, path []string, params *MarsJSON.JSONObject, body string) []byte {
	if body == "" {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}
	_ = jwt
	_ = params

	isPOST := strings.TrimSpace(body) != ""
	p := splitPathFromBase(r.URL.Path, "/auth")
	if len(p) == 0 {
		return mustJSON(ErrorResponse{Error: "not found"})
	}
	if p[0] == "web-config" {
		entry := strings.TrimSpace(h.WebEntryPath)
		if entry == "" || !strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "//") {
			entry = "/talk.html"
		}
		return mustJSON(map[string]string{"web_entry_path": entry})
	}
	if p[0] != "login" && p[0] != "projects" && p[0] != "devRegister" && p[0] != "devLogin" {
		if _, ok := requireAuthorizedRequest(r, jwt, h.Store); !ok {
			return mustJSON(ErrorResponse{Error: "unauthorized"})
		}
	}

	if len(p) >= 1 && p[0] == "login" {
		return h.handleLogin(w, r, body, isPOST)
	}
	if len(p) >= 1 && p[0] == "projects" {
		return h.handleProjects()
	}
	if len(p) >= 1 && p[0] == "devRegister" {
		return h.handleDevRegister(r, body, isPOST)
	}
	if len(p) >= 1 && p[0] == "devLogin" {
		return h.handleDevLogin(r, body, isPOST)
	}
	if len(p) >= 1 && p[0] == "registry" {
		if r.Method == http.MethodDelete {
			return h.handleRegistryDeleteHTTP(r, jwt, p)
		}
		return h.handleRegistryHTTP(w, r, jwt, p, body)
	}
	return mustJSON(ErrorResponse{Error: "not found"})
}

func (h *HttpAPI_auth) handleLogin(w http.ResponseWriter, r *http.Request, body string, isPOST bool) []byte {
	if !isPOST {
		return mustJSON(ErrorResponse{Error: "post required"})
	}

	var req AuthLoginRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return mustJSON(ErrorResponse{Error: "invalid json"})
	}
	req.Account = strings.TrimSpace(req.Account)
	req.Password = strings.TrimSpace(req.Password)
	req.Project = strings.TrimSpace(req.Project)
	if req.Account == "" || req.Password == "" {
		return mustJSON(ErrorResponse{Error: "missing account or password"})
	}

	proj := req.Project
	if proj == "" {
		proj = strings.TrimSpace(h.DefaultProj)
	}
	if proj == "" {
		proj = defaultLobbyProjectID
	}
	if strings.TrimSpace(h.MarsCloudURL) == "" {
		// 沒有 MARS Cloud 時，使用本機設定的預設帳密登入。
		// 離線登入只建立 SmallTalk 本機 session，不會核發 MARS Cloud token。
		if req.Account != strings.TrimSpace(h.DefaultAccount) || req.Password != h.DefaultPassword {
			return mustJSON(ErrorResponse{Error: "login failed"})
		}
	} else {
		client := MarsClient.Create()
		ok := client.LoginWithProj(h.MarsCloudURL, req.Account, req.Password, proj)
		if !ok {
			return mustJSON(ErrorResponse{Error: "login failed"})
		}
	}

	sessionToken, _, _, err := encodeSessionAuthToken(req.Account, loginSessionTTLSec)
	if err != nil {
		return mustJSON(ErrorResponse{Error: "create session failed"})
	}
	if h.Store != nil {
		_, _ = h.Store.UpsertAuthToken(AuthTokenRecord{
			Token:     sessionToken,
			ClientID:  req.Account,
			Kind:      "session-human",
			SourceIP:  sourceIPOf(r),
			IssuedAt:  time.Now().Format(time.RFC3339Nano),
			ExpiresAt: time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano),
		}, true)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "smalltalk_auth_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "smalltalk_account",
		Value:    req.Account,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "smalltalk_project",
		Value:    proj,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	return mustJSON(AuthLoginResponse{
		OK:        true,
		Account:   req.Account,
		Project:   proj,
		AuthToken: sessionToken,
	})
}

func (h *HttpAPI_auth) handleProjects() []byte {
	projects := h.listProjects()
	return mustJSON(AuthProjectsResponse{Projects: projects})
}

func (h *HttpAPI_auth) handleDevRegister(r *http.Request, body string, isPOST bool) []byte {
	if !isPOST {
		return mustJSON(ErrorResponse{Error: "post required"})
	}
	if h.Store == nil {
		return mustJSON(ErrorResponse{Error: "store not available"})
	}

	var req DevRegisterRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return mustJSON(ErrorResponse{Error: "invalid json"})
	}
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.MACAddress = normalizeMACAddress(req.MACAddress)
	if req.ClientID == "" || req.MACAddress == "" {
		return mustJSON(ErrorResponse{Error: "missing client_id or mac_address"})
	}
	if entry, ok := h.Store.GetAgentRegistry(req.ClientID); ok {
		if normalizeMACAddress(entry.MACAddress) == req.MACAddress {
			return h.handleDevLogin(r, body, isPOST)
		}
	}
	if req.DisplayName == "" {
		req.DisplayName = defaultDeviceDisplayName(req.MACAddress)
	}

	entry, err := h.Store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    req.ClientID,
		DisplayName: req.DisplayName,
		MACAddress:  req.MACAddress,
		LastSeenAt:  time.Now(),
		Meta: map[string]any{
			"source":    "dev-register",
			"source_ip": sourceIPOf(r),
		},
	})
	if err != nil {
		return mustJSON(ErrorResponse{Error: "registry failed"})
	}
	return mustJSON(DevRegisterResponse{
		OK:          true,
		Status:      devLoginStatusPending,
		Reason:      "registered",
		ClientID:    entry.ClientID,
		DisplayName: entry.DisplayName,
		MACAddress:  entry.MACAddress,
		Approved:    entry.Approved,
		Blocked:     entry.Blocked,
		TokenIssued: entry.TokenIssued,
		Message:     "註冊成功，請等待後台核發 Token。",
	})
}

func (h *HttpAPI_auth) handleDevLogin(r *http.Request, body string, isPOST bool) []byte {
	if !isPOST {
		return mustJSON(DevLoginResponse{
			OK:      false,
			Status:  devLoginStatusError,
			Reason:  "method_not_allowed",
			Message: "必須使用 POST。",
		})
	}
	if h.Store == nil {
		return mustJSON(DevLoginResponse{
			OK:      false,
			Status:  devLoginStatusError,
			Reason:  "store_unavailable",
			Message: "系統暫時無法提供登入服務。",
		})
	}

	var req DevLoginRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return mustJSON(DevLoginResponse{
			OK:      false,
			Status:  devLoginStatusError,
			Reason:  "invalid_json",
			Message: "登入資料格式錯誤。",
		})
	}
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.MACAddress = normalizeMACAddress(req.MACAddress)
	if req.ClientID == "" || req.MACAddress == "" {
		return mustJSON(DevLoginResponse{
			OK:         false,
			Status:     devLoginStatusError,
			Reason:     "missing_client_or_mac",
			ClientID:   req.ClientID,
			MACAddress: req.MACAddress,
			Message:    "缺少 client_id 或 mac_address。",
		})
	}

	entry, ok := h.Store.GetAgentRegistry(req.ClientID)
	if !ok {
		return mustJSON(DevLoginResponse{
			OK:         false,
			Status:     devLoginStatusUnregistered,
			Reason:     "never_registered",
			ClientID:   req.ClientID,
			MACAddress: req.MACAddress,
			Message:    "裝置從未註冊，請先完成註冊。",
		})
	}
	if entry.Blocked {
		return mustJSON(DevLoginResponse{
			OK:          false,
			Status:      devLoginStatusBlocked,
			Reason:      "account_blocked",
			ClientID:    entry.ClientID,
			DisplayName: entry.DisplayName,
			MACAddress:  entry.MACAddress,
			Approved:    entry.Approved,
			Blocked:     true,
			TokenIssued: entry.TokenIssued,
			Message:     "此裝置已被封鎖，請聯繫管理者。",
		})
	}
	if normalizeMACAddress(entry.MACAddress) != req.MACAddress {
		return mustJSON(DevLoginResponse{
			OK:          false,
			Status:      devLoginStatusError,
			Reason:      "mac_mismatch",
			ClientID:    entry.ClientID,
			DisplayName: entry.DisplayName,
			MACAddress:  req.MACAddress,
			Approved:    entry.Approved,
			Blocked:     entry.Blocked,
			TokenIssued: entry.TokenIssued,
			Message:     "MAC 地址不符。",
		})
	}
	if !entry.Approved || !entry.TokenIssued || strings.TrimSpace(entry.Token) == "" {
		return mustJSON(DevLoginResponse{
			OK:          false,
			Status:      devLoginStatusPending,
			Reason:      "awaiting_approval",
			ClientID:    entry.ClientID,
			DisplayName: entry.DisplayName,
			MACAddress:  entry.MACAddress,
			Approved:    entry.Approved,
			Blocked:     entry.Blocked,
			TokenIssued: entry.TokenIssued,
			Message:     "尚未核發 Token，請等待後台許可。",
		})
	}
	if !isShortDevToken(entry.Token) {
		token, now, exp, err := issueOrGetDevShortToken(h.Store, entry.ClientID)
		if err != nil {
			return mustJSON(DevLoginResponse{
				OK:          false,
				Status:      devLoginStatusError,
				Reason:      "issue_token_failed",
				ClientID:    entry.ClientID,
				DisplayName: entry.DisplayName,
				MACAddress:  entry.MACAddress,
				Approved:    entry.Approved,
				Blocked:     entry.Blocked,
				TokenIssued: entry.TokenIssued,
				Message:     "Token 核發失敗。",
			})
		}
		entry, err = h.Store.SetAgentIssuedToken(entry.ClientID, token, now, exp)
		if err != nil {
			return mustJSON(DevLoginResponse{
				OK:          false,
				Status:      devLoginStatusError,
				Reason:      "save_token_failed",
				ClientID:    entry.ClientID,
				DisplayName: entry.DisplayName,
				MACAddress:  entry.MACAddress,
				Approved:    entry.Approved,
				Blocked:     entry.Blocked,
				TokenIssued: entry.TokenIssued,
				Message:     "Token 儲存失敗。",
			})
		}
	}

	sourceIP := sourceIPOf(r)
	tokenRecord := AuthTokenRecord{
		Token:      strings.TrimSpace(entry.Token),
		ClientID:   entry.ClientID,
		Kind:       "dev-short",
		MACAddress: entry.MACAddress,
		SourceIP:   sourceIP,
		IssuedAt:   entry.TokenIssuedAt,
		ExpiresAt:  entry.TokenExpiresAt,
	}
	if tokenRecord.IssuedAt == "" {
		tokenRecord.IssuedAt = time.Now().Format(time.RFC3339Nano)
	}
	if tokenRecord.ExpiresAt == "" {
		tokenRecord.ExpiresAt = time.Now().Add(10 * 365 * 24 * time.Hour).Format(time.RFC3339Nano)
	}
	_, _ = h.Store.UpsertAuthToken(tokenRecord, true)
	_, _ = h.Store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    entry.ClientID,
		DisplayName: entry.DisplayName,
		MACAddress:  entry.MACAddress,
		LastSeenAt:  time.Now(),
		Meta: map[string]any{
			"dev_login_ip": sourceIP,
		},
	})
	return mustJSON(DevLoginResponse{
		OK:          true,
		Status:      devLoginStatusSuccess,
		Reason:      "login_ok",
		ClientID:    entry.ClientID,
		DisplayName: entry.DisplayName,
		MACAddress:  entry.MACAddress,
		Approved:    true,
		Blocked:     false,
		TokenIssued: true,
		Project:     strings.TrimSpace(h.DefaultProj),
		AuthToken:   strings.TrimSpace(entry.Token),
		Message:     "登入成功。",
	})
}

func (h *HttpAPI_auth) listProjects() []AuthProjectOption {
	seen := map[string]AuthProjectOption{}
	for _, candidate := range h.fetchProjectsFromMarsCloud() {
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			continue
		}
		candidate.ID = id
		if strings.TrimSpace(candidate.Name) == "" {
			candidate.Name = id
		}
		seen[id] = candidate
	}

	defaultProj := strings.TrimSpace(h.DefaultProj)
	if defaultProj != "" {
		if _, ok := seen[defaultProj]; !ok {
			seen[defaultProj] = AuthProjectOption{ID: defaultProj, Name: defaultProj}
		}
	}

	if len(seen) == 0 {
		seen[defaultLobbyProjectID] = AuthProjectOption{ID: defaultLobbyProjectID, Name: defaultLobbyProject}
	}

	out := make([]AuthProjectOption, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out
}

func (h *HttpAPI_auth) fetchProjectsFromMarsCloud() []AuthProjectOption {
	if h.MarsClient == nil || strings.TrimSpace(h.MarsClient.AuthToken) == "" {
		return nil
	}

	candidates := []string{
		"/api/project_list",
		"/auth/get_projects",
		"/auth/list_projects",
		"/api/get_projects",
		"/api/list_projects",
		"/sys/get_projects",
		"/sys/list_projects",
	}

	for _, api := range candidates {
		resp := strings.TrimSpace(h.MarsClient.CallAPI(api, "{}", 5000))
		if resp == "" {
			continue
		}
		if items := parseAuthProjectOptions(resp); len(items) > 0 {
			return items
		}
	}
	return nil
}

func parseAuthProjectOptions(raw string) []AuthProjectOption {
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return projectOptionsFromMaps(arr)
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}

	keys := []string{"projects", "results", "items", "data"}
	for _, key := range keys {
		list, ok := obj[key].([]any)
		if !ok {
			continue
		}
		items := make([]map[string]any, 0, len(list))
		for _, entry := range list {
			if m, ok := entry.(map[string]any); ok {
				items = append(items, m)
			}
		}
		if parsed := projectOptionsFromMaps(items); len(parsed) > 0 {
			return parsed
		}
	}
	return nil
}

func projectOptionsFromMaps(items []map[string]any) []AuthProjectOption {
	out := make([]AuthProjectOption, 0, len(items))
	for _, item := range items {
		id := firstString(item, "id", "proj", "project", "project_id", "code", "name")
		if id == "" {
			continue
		}
		name := firstString(item, "name", "title", "label", "display_name")
		out = append(out, AuthProjectOption{ID: id, Name: name})
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func normalizeMACAddress(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	replacer := strings.NewReplacer(":", "", "-", "", ".", "", " ", "")
	return replacer.Replace(raw)
}

func defaultDeviceDisplayName(mac string) string {
	mac = normalizeMACAddress(mac)
	if len(mac) >= 4 {
		return mac[len(mac)-4:]
	}
	if mac != "" {
		return mac
	}
	return "device"
}

func issueOrGetDevShortToken(store *Store, clientID string) (string, time.Time, time.Time, error) {
	if store == nil {
		return "", time.Time{}, time.Time{}, ErrMissingClientID
	}
	entry, ok := store.GetAgentRegistry(clientID)
	if ok && entry.TokenIssued && isShortDevToken(entry.Token) {
		issuedAt, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.TokenIssuedAt))
		expiresAt, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.TokenExpiresAt))
		if issuedAt.IsZero() {
			issuedAt = time.Now()
		}
		if expiresAt.IsZero() {
			expiresAt = issuedAt.Add(10 * 365 * 24 * time.Hour)
		}
		return strings.TrimSpace(entry.Token), issuedAt, expiresAt, nil
	}

	token, err := generateShortDevToken(16)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	now := time.Now()
	exp := now.Add(10 * 365 * 24 * time.Hour)
	return token, now, exp, nil
}

func isShortDevToken(token string) bool {
	token = strings.TrimSpace(token)
	return token != "" && len(token) < 20 && !strings.ContainsAny(token, " \t\r\n")
}

func generateShortDevToken(length int) (string, error) {
	if length <= 0 {
		length = 16
	}
	size := (length * 5 / 8) + 2
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), "=")
	token = strings.ToLower(token)
	if len(token) > length {
		token = token[:length]
	}
	return token, nil
}
