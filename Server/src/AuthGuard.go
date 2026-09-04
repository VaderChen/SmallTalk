package main

import (
	"net"
	"net/http"
	"net/url"
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

func hasURLCredential(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	query := r.URL.Query()
	return strings.TrimSpace(query.Get("token")) != "" || strings.TrimSpace(query.Get("auth_token")) != ""
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
					if !strings.EqualFold(strings.TrimSpace(payload.ClientID), strings.TrimSpace(record.ClientID)) {
						continue
					}
					nowUnix := time.Now().Unix()
					if payload.ExpireAt <= payload.IssuedAt || payload.ExpireAt < nowUnix || payload.IssuedAt > nowUnix+300 {
						continue
					}
					expectedPurpose := "smalltalk-client-auth"
					if strings.EqualFold(strings.TrimSpace(record.Kind), "session-human") {
						expectedPurpose = "smalltalk-session-auth"
					}
					if payload.Purpose != expectedPurpose {
						continue
					}
					principalType := "human"
					if strings.EqualFold(strings.TrimSpace(record.Kind), "dev-short") || strings.EqualFold(strings.TrimSpace(record.Kind), "agent") {
						principalType = "agent"
					}
					if strings.EqualFold(strings.TrimSpace(record.ClientID), "root") || isAgentAdmin(store, record.ClientID) {
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
				principalType := "agent"
				if strings.EqualFold(strings.TrimSpace(record.Kind), "session-human") {
					principalType = "human"
				}
				if strings.EqualFold(strings.TrimSpace(record.ClientID), "root") || isAgentAdmin(store, record.ClientID) {
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

		// A decryptable or correctly signed payload is not sufficient by itself:
		// the exact token must also be active in the authoritative token store.
		// This makes rotation, revocation, blocking and registry deletion effective.
		continue
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
		strings.TrimSpace(r.Header.Get("X-SmallTalk-Token")),
		strings.TrimSpace(r.Header.Get("X-Auth-Token")),
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

func hasHeaderCredential(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Authorization")) != "" ||
		strings.TrimSpace(r.Header.Get("Authentication")) != "" ||
		strings.TrimSpace(r.Header.Get("X-SmallTalk-Token")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Auth-Token")) != ""
}

func isSafeCookieMutation(r *http.Request, store *Store) bool {
	if r == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if _, err := r.Cookie("smalltalk_auth_token"); err != nil {
		return true
	}
	if hasHeaderCredential(r) {
		headerOnly := r.Clone(r.Context())
		headerOnly.Header = r.Header.Clone()
		headerOnly.Header.Del("Cookie")
		if _, ok := requireAuthorizedRequest(headerOnly, nil, store); ok {
			return true
		}
	}
	rawOrigin := strings.TrimSpace(r.Header.Get("Origin"))
	if rawOrigin == "" {
		rawOrigin = strings.TrimSpace(r.Header.Get("Referer"))
	}
	parsed, err := url.Parse(rawOrigin)
	if err != nil || parsed.Host == "" {
		return false
	}
	expectedScheme := "http"
	if requestUsesHTTPS(r, store) {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Host, r.Host) && strings.EqualFold(parsed.Scheme, expectedScheme)
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
	if store == nil || !store.isTrustedProxy(peer) {
		return peer
	}
	for _, key := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
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

func isAgentAdmin(store *Store, clientID string) bool {
	if store == nil {
		return false
	}
	entry, ok := store.GetAgentRegistry(clientID)
	return ok && entry.IsAdmin
}

func isRegisteredAgentSource(entry AgentRegistryEntry, sourceIP string) bool {
	sourceIP = strings.TrimSpace(sourceIP)
	if sourceIP == "" || entry.Meta == nil {
		return false
	}
	for _, key := range []string{"source_ip", "dev_login_ip"} {
		if recorded, ok := entry.Meta[key].(string); ok && isSameSubnetOrLocal(recorded, sourceIP) {
			return true
		}
	}
	return false
}
