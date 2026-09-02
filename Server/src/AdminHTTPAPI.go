package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
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
	if len(parts) == 1 && parts[0] == "rooms" {
		if r.Method != http.MethodGet {
			return mustJSON(ErrorResponse{Error: "method not allowed"})
		}
		return mustJSON(api.Store.ListAllRooms(time.Now()))
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
		return mustJSON(map[string]any{"agents": h.Store.ListAgentRegistry()})
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
