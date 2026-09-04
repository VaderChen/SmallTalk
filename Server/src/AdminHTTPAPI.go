package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
)

// PermissionsAPI exposes the root-only HTTP API used by the permissions page.
type PermissionsAPI struct {
	Store *Store
}

func requireHTTPRoot(r *http.Request, jwt *MarsJSON.JSONObject, store *Store) error {
	principal, ok := requireAuthorizedRequest(r, jwt, store)
	if !ok {
		return fmt.Errorf("unauthorized")
	}
	if !principal.IsRoot() {
		return ErrForbidden
	}
	return nil
}

func (api *PermissionsAPI) Process(w http.ResponseWriter, r *http.Request, jwt *MarsJSON.JSONObject, _ []string, _ *MarsJSON.JSONObject, body string) []byte {
	if api == nil || api.Store == nil {
		return mustJSON(ErrorResponse{Error: "store not available"})
	}
	if hasURLCredential(r) {
		if w != nil {
			w.WriteHeader(http.StatusBadRequest)
		}
		return mustJSON(ErrorResponse{Error: "credentials in URLs are not allowed"})
	}
	if !isSafeCookieMutation(r, api.Store) {
		if w != nil {
			w.WriteHeader(http.StatusForbidden)
		}
		return mustJSON(ErrorResponse{Error: "cross-origin request rejected"})
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && strings.TrimSpace(body) == "" && r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, maxAuthRequestBodyBytes+1))
		if err != nil || len(data) > maxAuthRequestBodyBytes {
			if w != nil {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
			}
			return mustJSON(ErrorResponse{Error: "request body too large"})
		}
		body = string(data)
	} else if len(body) > maxAuthRequestBodyBytes {
		if w != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
		return mustJSON(ErrorResponse{Error: "request body too large"})
	}
	if err := requireHTTPRoot(r, jwt, api.Store); err != nil {
		return mustJSON(ErrorResponse{Error: err.Error()})
	}

	parts := splitPathFromBase(r.URL.Path, "/permissions")
	if len(parts) == 1 && parts[0] == "auto-approval" {
		switch r.Method {
		case http.MethodGet:
			return mustJSON(map[string]any{
				"enabled":          api.Store.AutoApprovalEnabled(),
				"interval_minutes": api.Store.AutoApprovalIntervalMinutes(),
				"interval":         fmt.Sprintf("%dm0s", api.Store.AutoApprovalIntervalMinutes()),
			})
		case http.MethodPost:
			if strings.TrimSpace(body) == "" && r.Body != nil {
				data, _ := io.ReadAll(r.Body)
				body = string(data)
			}
			var req struct {
				Enabled         *bool `json:"enabled"`
				IntervalMinutes *int  `json:"interval_minutes"`
			}
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				return mustJSON(ErrorResponse{Error: "invalid json"})
			}
			enabled := api.Store.AutoApprovalEnabled()
			if req.Enabled != nil {
				enabled = *req.Enabled
			}
			intervalMin := api.Store.AutoApprovalIntervalMinutes()
			if req.IntervalMinutes != nil && *req.IntervalMinutes > 0 {
				intervalMin = *req.IntervalMinutes
			}
			if err := api.Store.SetAutoApprovalConfig(enabled, intervalMin); err != nil {
				return mustJSON(ErrorResponse{Error: "save auto approval setting failed"})
			}
			return mustJSON(map[string]any{
				"enabled":          api.Store.AutoApprovalEnabled(),
				"interval_minutes": api.Store.AutoApprovalIntervalMinutes(),
				"interval":         fmt.Sprintf("%dm0s", api.Store.AutoApprovalIntervalMinutes()),
			})
		default:
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
	}
	if len(parts) == 1 && parts[0] == "system-policy" {
		switch r.Method {
		case http.MethodGet:
			return mustJSON(api.Store.GetSystemPolicy())
		case http.MethodPost:
			if strings.TrimSpace(body) == "" && r.Body != nil {
				data, _ := io.ReadAll(r.Body)
				body = string(data)
			}
			var req SystemPolicyConfig
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				return mustJSON(ErrorResponse{Error: "invalid json"})
			}
			if err := api.Store.SetSystemPolicy(req); err != nil {
				return mustJSON(ErrorResponse{Error: "save system policy failed: " + err.Error()})
			}
			return mustJSON(api.Store.GetSystemPolicy())
		default:
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
	}
	if len(parts) == 1 && (parts[0] == "admin-password" || parts[0] == "backend-password") {
		if r.Method != http.MethodPost {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		if strings.TrimSpace(body) == "" && r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			body = string(data)
		}
		var req struct {
			OldPassword     string `json:"old_password"`
			NewPassword     string `json:"new_password"`
			ConfirmPassword string `json:"confirm_password"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return mustJSON(ErrorResponse{Error: "資料格式錯誤"})
		}
		req.OldPassword = strings.TrimSpace(req.OldPassword)
		req.NewPassword = strings.TrimSpace(req.NewPassword)
		req.ConfirmPassword = strings.TrimSpace(req.ConfirmPassword)

		if req.OldPassword == "" {
			return mustJSON(ErrorResponse{Error: "請輸入目前密碼"})
		}
		if len([]rune(req.NewPassword)) < minAdminPasswordLength {
			return mustJSON(ErrorResponse{Error: fmt.Sprintf("新密碼長度至少需 %d 個字元", minAdminPasswordLength)})
		}
		if req.ConfirmPassword != "" && req.NewPassword != req.ConfirmPassword {
			return mustJSON(ErrorResponse{Error: "兩次輸入的新密碼不一致"})
		}

		if !api.Store.VerifyAdminPassword(req.OldPassword) {
			return mustJSON(ErrorResponse{Error: "目前密碼輸入錯誤，請重新確認"})
		}

		if err := api.Store.SetAdminPassword(req.NewPassword); err != nil {
			return mustJSON(ErrorResponse{Error: "更新密碼失敗: " + err.Error()})
		}
		return mustJSON(map[string]any{
			"ok":      true,
			"message": "後端管理者密碼已成功更新！請妥善保管新密碼。",
		})
	}
	if len(parts) == 1 && parts[0] == "rooms" {
		if r.Method != http.MethodGet {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		return mustJSON(api.Store.ListAllRooms(time.Now()))
	}
	if len(parts) >= 2 && parts[0] == "rooms" && parts[1] == "update" {
		if r.Method != http.MethodPost {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		if strings.TrimSpace(body) == "" && r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			body = string(data)
		}
		var req struct {
			ProjectID   string `json:"project_id"`
			RoomID      string `json:"room_id"`
			Room        string `json:"room"`
			Name        string `json:"name"`
			Category    string `json:"category"`
			Description string `json:"description"`
			Owner       string `json:"owner"`
			Pinned      *bool  `json:"pinned"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return mustJSON(ErrorResponse{Error: "invalid json"})
		}
		roomStr := firstNonEmpty(req.Room, req.RoomID)
		projectID := req.ProjectID
		roomID := req.RoomID
		if strings.Contains(roomStr, "/") {
			p := strings.SplitN(roomStr, "/", 2)
			projectID = p[0]
			roomID = p[1]
		} else if projectID == "" {
			projectID = "default"
			roomID = roomStr
		}
		updated, err := api.Store.UpdateRoomFull(projectID, roomID, req.Name, req.Category, req.Description, req.Owner, req.Pinned)
		if err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(map[string]any{"ok": true, "room": updated})
	}
	if len(parts) >= 2 && parts[0] == "rooms" && (parts[1] == "create" || parts[1] == "new") {
		if r.Method != http.MethodPost {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		if strings.TrimSpace(body) == "" && r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			body = string(data)
		}
		var req struct {
			ProjectID   string `json:"project_id"`
			RoomID      string `json:"room_id"`
			Room        string `json:"room"`
			Name        string `json:"name"`
			Category    string `json:"category"`
			Description string `json:"description"`
			Owner       string `json:"owner"`
			Pinned      bool   `json:"pinned"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return mustJSON(ErrorResponse{Error: "invalid json"})
		}
		roomID := strings.TrimSpace(firstNonEmpty(req.RoomID, req.Room))
		projectID := strings.TrimSpace(req.ProjectID)
		if strings.Contains(roomID, "/") {
			p := strings.SplitN(roomID, "/", 2)
			projectID = p[0]
			roomID = p[1]
		} else if projectID == "" {
			projectID = "default"
		}
		if roomID == "" {
			return mustJSON(ErrorResponse{Error: "看板代碼 (Room ID) 不可為空"})
		}
		matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, roomID)
		if !matched {
			return mustJSON(ErrorResponse{Error: "看板代碼僅限英數字、連字號與底線 (a-z, 0-9, -, _)"})
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = roomID
		}
		newRoom, err := api.Store.CreateRoomFull(projectID, roomID, name, strings.TrimSpace(req.Category), strings.TrimSpace(req.Description), strings.TrimSpace(req.Owner), req.Pinned)
		if err != nil {
			if errors.Is(err, ErrAlreadyExists) {
				return mustJSON(ErrorResponse{Error: "此看板代碼已存在，請使用其他代碼"})
			}
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(map[string]any{"ok": true, "room": newRoom})
	}
	if len(parts) == 2 && parts[1] == "read-only" {
		if r.Method != http.MethodPost {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		clientID := strings.TrimSpace(parts[0])
		if strings.TrimSpace(body) == "" && r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			body = string(data)
		}
		var req struct {
			ReadOnly bool `json:"read_only"`
		}
		if strings.TrimSpace(body) != "" {
			_ = json.Unmarshal([]byte(body), &req)
		}
		entry, err := api.Store.SetAgentReadOnly(clientID, req.ReadOnly, time.Now())
		if err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(entry)
	}
	if len(parts) == 2 && (parts[1] == "delete" || parts[1] == "remove") {
		clientID := strings.TrimSpace(parts[0])
		if err := api.Store.DeleteAgentRegistry(clientID); err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(map[string]any{"ok": true, "client_id": clientID})
	}
	if len(parts) == 2 && parts[1] == "role" {
		clientID := strings.TrimSpace(parts[0])
		switch r.Method {
		case http.MethodGet:
			isAdmin, modRooms, err := api.Store.GetAgentRole(clientID)
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			displayName := ""
			if entry, ok := api.Store.GetAgentRegistry(clientID); ok {
				displayName = entry.DisplayName
			}
			return mustJSON(map[string]any{
				"ok":              true,
				"client_id":       clientID,
				"display_name":    displayName,
				"is_admin":        isAdmin,
				"moderator_rooms": modRooms,
			})
		case http.MethodPost:
			if strings.TrimSpace(body) == "" && r.Body != nil {
				data, _ := io.ReadAll(r.Body)
				body = string(data)
			}
			var req struct {
				IsAdmin        bool     `json:"is_admin"`
				ModeratorRooms []string `json:"moderator_rooms"`
			}
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				return mustJSON(ErrorResponse{Error: "invalid json"})
			}
			if err := api.Store.SetAgentRole(clientID, req.IsAdmin, req.ModeratorRooms); err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			isAdmin, modRooms, _ := api.Store.GetAgentRole(clientID)
			return mustJSON(map[string]any{
				"ok":              true,
				"client_id":       clientID,
				"is_admin":        isAdmin,
				"moderator_rooms": modRooms,
			})
		default:
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
	}
	if len(parts) != 1 {
		return mustJSON(ErrorResponse{Error: "not found"})
	}

	clientID := strings.TrimSpace(parts[0])
	switch r.Method {
	case http.MethodGet:
		acl, _ := api.Store.GetClientACL(clientID)
		return mustJSON(acl)
	case http.MethodDelete:
		if err := api.Store.DeleteAgentRegistry(clientID); err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(map[string]any{"ok": true, "client_id": clientID})
	case http.MethodPost:
		if strings.TrimSpace(body) == "" && r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			body = string(data)
		}
		var req UpdateClientACLRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return mustJSON(ErrorResponse{Error: "invalid json"})
		}
		if req.ClientID != "" && strings.TrimSpace(req.ClientID) != clientID {
			return mustJSON(ErrorResponse{Error: "client_id mismatch"})
		}
		if err := api.Store.UpsertClientACL(clientID, req.AllowRooms, req.DenyRooms); err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		acl, _ := api.Store.GetClientACL(clientID)
		return mustJSON(acl)
	default:
		return mustJSON(ErrorResponse{Error: "method not allowed"})
	}
}

func (h *HttpAPI_auth) handleRegistryHTTP(w http.ResponseWriter, r *http.Request, jwt *MarsJSON.JSONObject, path []string, body string) []byte {
	if h == nil || h.Store == nil {
		return mustJSON(ErrorResponse{Error: "store not available"})
	}
	if err := requireHTTPRoot(r, jwt, h.Store); err != nil {
		return mustJSON(ErrorResponse{Error: err.Error()})
	}

	if len(path) == 1 {
		if r.Method != http.MethodGet {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		return mustJSON(map[string]any{"agents": h.Store.ListAgentRegistryRedacted()})
	}
	if len(path) == 3 && (path[2] == "delete" || path[2] == "remove") {
		clientID := strings.TrimSpace(path[1])
		if err := h.Store.DeleteAgentRegistry(clientID); err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(map[string]any{"ok": true, "client_id": clientID})
	}
	if len(path) == 2 && r.Method == http.MethodDelete {
		clientID := strings.TrimSpace(path[1])
		if err := h.Store.DeleteAgentRegistry(clientID); err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(map[string]any{"ok": true, "client_id": clientID})
	}
	if len(path) == 3 && path[2] == "read-only" {
		if r.Method != http.MethodPost {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		clientID := strings.TrimSpace(path[1])
		if strings.TrimSpace(body) == "" && r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			body = string(data)
		}
		var req struct {
			ReadOnly bool `json:"read_only"`
		}
		if strings.TrimSpace(body) != "" {
			_ = json.Unmarshal([]byte(body), &req)
		}
		entry, err := h.Store.SetAgentReadOnly(clientID, req.ReadOnly, time.Now())
		if err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		return mustJSON(entry)
	}
	if len(path) != 3 || path[2] != "issue" {
		return mustJSON(ErrorResponse{Error: "not found"})
	}
	if r.Method != http.MethodPost {
		return mustJSON(ErrorResponse{Error: "method not allowed"})
	}

	clientID := strings.TrimSpace(path[1])
	entry, ok := h.Store.GetAgentRegistry(clientID)
	if !ok {
		return mustJSON(ErrorResponse{Error: "agent not found"})
	}

	if strings.TrimSpace(body) == "" && r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		body = string(data)
	}
	var req AuthIssueTokenRequest
	if strings.TrimSpace(body) != "" {
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return mustJSON(ErrorResponse{Error: "invalid json"})
		}
	}
	_ = req

	entry, err := issueApprovedAgentToken(h.Store, clientID)
	if err != nil {
		return mustJSON(ErrorResponse{Error: err.Error()})
	}
	token := entry.Token
	issuedAt, _ := time.Parse(time.RFC3339Nano, entry.TokenIssuedAt)
	expiresAt, _ := time.Parse(time.RFC3339Nano, entry.TokenExpiresAt)
	return mustJSON(map[string]any{
		"client_id":  clientID,
		"token":      token,
		"issued_at":  issuedAt.Format(time.RFC3339Nano),
		"expires_at": expiresAt.Format(time.RFC3339Nano),
	})
}

func issueApprovedAgentToken(store *Store, clientID string) (AgentRegistryEntry, error) {
	if store == nil {
		return AgentRegistryEntry{}, fmt.Errorf("store not available")
	}
	entry, ok := store.GetAgentRegistry(clientID)
	if !ok {
		return AgentRegistryEntry{}, fmt.Errorf("agent not found")
	}
	if entry.Blocked {
		return AgentRegistryEntry{}, fmt.Errorf("agent is blocked")
	}
	token, issuedAt, expiresAt, err := issueOrGetDevShortToken(store, clientID)
	if err != nil {
		return AgentRegistryEntry{}, fmt.Errorf("issue token failed")
	}
	entry, err = store.SetAgentIssuedToken(clientID, token, issuedAt, expiresAt)
	if err != nil {
		return AgentRegistryEntry{}, fmt.Errorf("save token failed")
	}
	entry, err = store.SetAgentApproval(clientID, true, issuedAt)
	if err != nil {
		return AgentRegistryEntry{}, fmt.Errorf("save approval failed")
	}
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token:      token,
		ClientID:   clientID,
		Kind:       "dev-short",
		MACAddress: entry.MACAddress,
		IssuedAt:   issuedAt.Format(time.RFC3339Nano),
		ExpiresAt:  expiresAt.Format(time.RFC3339Nano),
	}, true); err != nil {
		return AgentRegistryEntry{}, fmt.Errorf("save auth token failed")
	}
	return entry, nil
}

func (h *HttpAPI_auth) handleRegistryDeleteHTTP(r *http.Request, jwt *MarsJSON.JSONObject, path []string) []byte {
	if h == nil || h.Store == nil {
		return mustJSON(ErrorResponse{Error: "store not available"})
	}
	if err := requireHTTPRoot(r, jwt, h.Store); err != nil {
		return mustJSON(ErrorResponse{Error: err.Error()})
	}
	if len(path) != 2 || r.Method != http.MethodDelete {
		return mustJSON(ErrorResponse{Error: "not found"})
	}
	clientID := strings.TrimSpace(path[1])
	if err := h.Store.DeleteAgentRegistry(clientID); err != nil {
		return mustJSON(ErrorResponse{Error: err.Error()})
	}
	return mustJSON(map[string]any{"ok": true, "client_id": clientID})
}
