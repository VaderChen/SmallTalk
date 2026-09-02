package main

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Security"
)

type requestAuthContext struct {
	// Kind is kept for compatibility with existing HTTP handlers.
	Kind          string
	PrincipalType string // agent, human, or root
	TokenKind     string
	ClientID      string
	SourceIP      string
	JWT           *MarsJSON.JSONObject
}

func (ctx *requestAuthContext) IsSystem() bool {
	return ctx != nil && strings.EqualFold(strings.TrimSpace(ctx.ClientID), "root") && strings.EqualFold(strings.TrimSpace(ctx.TokenKind), "system")
}

func (ctx *requestAuthContext) IsRoot() bool {
	if ctx == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ctx.ClientID), "root") || strings.EqualFold(strings.TrimSpace(ctx.PrincipalType), "root")
}

func requireAuthorizedRequest(r *http.Request, jwt *MarsJSON.JSONObject, store *Store) (*requestAuthContext, bool) {
	if jwt != nil && jwt.Length() > 0 {
		clientID := marsCloudClientID(jwt)
		if clientID == "" {
			return nil, false
		}
		return &requestAuthContext{Kind: "marscloud", PrincipalType: "human", ClientID: clientID, JWT: jwt, SourceIP: sourceIPOfWithStore(r, store)}, true
	}

	sourceIP := sourceIPOfWithStore(r, store)
	tokens := candidateAuthTokens(r)
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if marsJWT := Security.VerifyToken(token, "", remoteAddrOf(r)); marsJWT != nil && marsJWT.Length() > 0 {
			clientID := marsCloudClientID(marsJWT)
			if clientID != "" {
				return &requestAuthContext{Kind: "marscloud", PrincipalType: "human", ClientID: clientID, JWT: marsJWT, SourceIP: sourceIP}, true
			}
		}

		if store != nil {
			if record, ok := store.AuthorizeAuthToken(token, sourceIP); ok {
				if entry, exists := store.GetAgentRegistry(record.ClientID); exists && entry.Blocked {
					continue
				}
				if payload, err := decodeClientAuthToken(token); err == nil && payload != nil {
					if (payload.ExpireAt == 0 || payload.ExpireAt >= time.Now().Unix()) &&
						strings.EqualFold(strings.TrimSpace(payload.ClientID), strings.TrimSpace(record.ClientID)) {
						principalType := "human"
						if strings.EqualFold(strings.TrimSpace(record.Kind), "dev-short") || strings.EqualFold(strings.TrimSpace(record.Kind), "agent") {
							principalType = "agent"
						}
						if strings.EqualFold(strings.TrimSpace(record.ClientID), "root") {
							principalType = "root"
						}
						return &requestAuthContext{
							Kind:          "smalltalk",
							PrincipalType: principalType,
							TokenKind:     strings.TrimSpace(record.Kind),
							ClientID:      strings.TrimSpace(record.ClientID),
							SourceIP:      sourceIP,
						}, true
					}
				}
				if strings.EqualFold(strings.TrimSpace(record.Kind), "session-human") {
					principalType := "human"
					if strings.EqualFold(strings.TrimSpace(record.ClientID), "root") {
						principalType = "root"
					}
					return &requestAuthContext{
						Kind:          "smalltalk-session",
						PrincipalType: principalType,
						TokenKind:     strings.TrimSpace(record.Kind),
						ClientID:      strings.TrimSpace(record.ClientID),
						SourceIP:      sourceIP,
					}, true
				}

				if strings.EqualFold(strings.TrimSpace(record.Kind), "dev-short") || strings.EqualFold(strings.TrimSpace(record.Kind), "system") || strings.EqualFold(strings.TrimSpace(record.Kind), "agent") {
					principalType := "agent"
					if strings.EqualFold(strings.TrimSpace(record.ClientID), "root") {
						principalType = "root"
					}
					return &requestAuthContext{
						Kind:          "smalltalk-dev",
						PrincipalType: principalType,
						TokenKind:     strings.TrimSpace(record.Kind),
						ClientID:      strings.TrimSpace(record.ClientID),
						SourceIP:      sourceIP,
					}, true
				}
			}
		}

		payload, err := decodeClientAuthToken(token)
		if err != nil || payload == nil {
			continue
		}
		if payload.Purpose != "smalltalk-client-auth" && payload.Purpose != "smalltalk-session-auth" {
			continue
		}
		if payload.ExpireAt > 0 && payload.ExpireAt < time.Now().Unix() {
			continue
		}
		clientID := strings.TrimSpace(payload.ClientID)
		if clientID == "" {
			continue
		}
	}

	if len(tokens) == 0 && store != nil {
		for _, macAddress := range candidateDeviceMACs(r) {
			if entry, ok := store.FindTrustedAgentByMACAndIP(macAddress, sourceIP); ok {
				principalType := "agent"
				if strings.EqualFold(strings.TrimSpace(entry.ClientID), "root") {
					principalType = "root"
				}
				return &requestAuthContext{
					Kind:          "smalltalk-dev-trusted",
					PrincipalType: principalType,
					TokenKind:     "trusted-device",
					ClientID:      strings.TrimSpace(entry.ClientID),
					SourceIP:      sourceIP,
				}, true
			}
		}
	}

	return nil, false
}

func candidateAuthTokens(r *http.Request) []string {
	if r == nil {
		return nil
	}

	values := []string{
		extractBearerLikeToken(r.Header.Get("Authentication")),
		extractBearerLikeToken(r.Header.Get("Authorization")),
	}

	if cookie, err := r.Cookie("smalltalk_auth_token"); err == nil {
		values = append(values, strings.TrimSpace(cookie.Value))
	}

	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func extractBearerLikeToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Fields(raw)
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return raw
}

func candidateDeviceMACs(r *http.Request) []string {
	if r == nil {
		return nil
	}
	values := []string{
		normalizeMACAddress(strings.TrimSpace(r.Header.Get("X-MAC-Address"))),
		normalizeMACAddress(strings.TrimSpace(r.Header.Get("X-Device-MAC"))),
		normalizeMACAddress(strings.TrimSpace(r.Header.Get("X-Client-MAC"))),
		normalizeMACAddress(strings.TrimSpace(r.URL.Query().Get("mac_address"))),
		normalizeMACAddress(strings.TrimSpace(r.URL.Query().Get("mac"))),
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, item := range values {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func remoteAddrOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.RemoteAddr
}

func sourceIPOf(r *http.Request) string { return sourceIPOfWithStore(r, nil) }

func sourceIPOfWithStore(r *http.Request, store *Store) string {
	if r == nil {
		return ""
	}
	peer := remoteHost(r)
	if store != nil && store.isTrustedProxy(peer) {
		for _, key := range []string{"X-Forwarded-For", "X-Real-IP"} {
			raw := strings.TrimSpace(r.Header.Get(key))
			if raw == "" {
				continue
			}
			if key == "X-Forwarded-For" && strings.Contains(raw, ",") {
				raw = strings.TrimSpace(strings.Split(raw, ",")[0])
			}
			if net.ParseIP(raw) != nil {
				return raw
			}
		}
	}
	return peer
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func marsCloudClientID(jwt *MarsJSON.JSONObject) string {
	if jwt == nil {
		return ""
	}
	for _, key := range []string{"client_id", "clientId", "sub", "account", "username", "user_id", "id"} {
		if value := strings.TrimSpace(jwt.OptString(key, "")); value != "" {
			return value
		}
	}
	return ""
}
