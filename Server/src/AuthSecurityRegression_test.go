package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
)

func TestMACHeadersAloneNeverAuthorize(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	entry, err := store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID: "agent-a", MACAddress: "02:00:00:00:00:01", LastSeenAt: time.Now(),
		Meta: map[string]any{"source_ip": "192.0.2.10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.Approved = true
	store.mu.Lock()
	store.agentRegistry[entry.ClientID] = &entry
	store.mu.Unlock()

	r := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	r.Header.Set("X-MAC-Address", "02:00:00:00:00:01")
	if principal, ok := requireAuthorizedRequest(r, nil, store); ok || principal != nil {
		t.Fatalf("MAC-only request was authorized: %#v", principal)
	}
}

func TestQueryStringTokenIsIgnored(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token: "secret-token", ClientID: "root", Kind: "session-human",
		IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://example.test/api?auth_token=secret-token", nil)
	if principal, ok := requireAuthorizedRequest(r, nil, store); ok || principal != nil {
		t.Fatalf("query-string token was authorized: %#v", principal)
	}

	h := &HttpAPI_auth{Store: store}
	r = httptest.NewRequest(http.MethodGet, "http://example.test/auth/session?token=signed-but-leaked", nil)
	w := httptest.NewRecorder()
	resp := string(h.Process(w, r, MarsJSON.NewJSONObject(map[string]any{"sub": "root"}), nil, nil, ""))
	if w.Code != http.StatusBadRequest || !strings.Contains(resp, "credentials in URLs are not allowed") {
		t.Fatalf("framework-provided query credential was not rejected: status=%d response=%s", w.Code, resp)
	}
}

func TestMalformedTokenExpiryFailsClosed(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token: "malformed-expiry", ClientID: "root", Kind: "session-human",
		IssuedAt: time.Now().Format(time.RFC3339Nano), ExpiresAt: "not-a-timestamp",
	}, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://example.test/api", nil)
	r.Header.Set("Authorization", "Bearer malformed-expiry")
	if principal, ok := requireAuthorizedRequest(r, nil, store); ok || principal != nil {
		t.Fatalf("token with malformed expiry was authorized: %#v", principal)
	}
}

func TestWeakConfiguredAdminPasswordIsDisabled(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	store.SetDefaultAdminPassword("root")
	if store.VerifyAdminPassword("root") {
		t.Fatal("weak configured administrator password was accepted")
	}
	store.SetDefaultAdminPassword("strong-password-123")
	if !store.VerifyAdminPassword("strong-password-123") {
		t.Fatal("valid configured administrator password was rejected")
	}
}

func TestCookieMutationRequiresSameOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "https://bbs.example/api/articles", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: "smalltalk_auth_token", Value: "secret"})
	r.Header.Set("Origin", "https://evil.example")
	if isSafeCookieMutation(r, nil) {
		t.Fatal("cross-origin cookie mutation was accepted")
	}
	r.Header.Set("Origin", "https://bbs.example")
	if !isSafeCookieMutation(r, nil) {
		t.Fatal("same-origin cookie mutation was rejected")
	}
}

func TestSourceIPIgnoresUntrustedForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	r.Header.Set("CF-Connecting-IP", "127.0.0.1")
	if got := sourceIPOf(r); got != "192.0.2.10" {
		t.Fatalf("sourceIPOf=%q, want TCP peer IP", got)
	}
}

func TestSourceIPAcceptsForwardedHeadersOnlyFromTrustedProxy(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if err := store.ConfigureSecurity(nil, []string{"192.0.2.0/24"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	r.Header.Set("CF-Connecting-IP", "198.51.100.20")
	if got := sourceIPOfWithStore(r, store); got != "198.51.100.20" {
		t.Fatalf("trusted proxy source=%q", got)
	}
}

func TestForgedUnsignedJWTNeverCreatesAuthorization(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	entry, err := store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID: "admin-agent", DisplayName: "Admin", LastSeenAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.Approved = true
	entry.IsAdmin = true
	store.mu.Lock()
	store.agentRegistry[entry.ClientID] = &entry
	store.mu.Unlock()

	r := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	r.Header.Set("Authorization", "Bearer eyJhbGciOiJub25lIn0.eyJzdWIiOiJhZG1pbi1hZ2VudCJ9.")
	if principal, ok := requireAuthorizedRequest(r, nil, store); ok || principal != nil {
		t.Fatalf("forged JWT was accepted: %#v", principal)
	}
	if records := store.ListAuthTokenRecords(); len(records) != 0 {
		t.Fatalf("forged JWT was persisted: %#v", records)
	}
}

func TestMCPOriginAllowlist(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	h := NewMCPHTTPHandler(&SmallTalkFacade{Store: store})

	allowed := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "http://example.test")
	h.ServeHTTP(allowed, r)
	if allowed.Code != http.StatusNoContent || allowed.Header().Get("Access-Control-Allow-Origin") != "http://example.test" {
		t.Fatalf("allowed origin response: status=%d headers=%v", allowed.Code, allowed.Header())
	}

	defaultBlocked := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(defaultBlocked, r)
	if defaultBlocked.Code != http.StatusForbidden || defaultBlocked.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("default-blocked origin response: status=%d headers=%v", defaultBlocked.Code, defaultBlocked.Header())
	}

	if err := store.ConfigureSecurity([]string{"http://localhost:18790"}, nil); err != nil {
		t.Fatal(err)
	}
	configuredAllowed := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "http://localhost:18790")
	h.ServeHTTP(configuredAllowed, r)
	if configuredAllowed.Code != http.StatusNoContent || configuredAllowed.Header().Get("Access-Control-Allow-Origin") != "http://localhost:18790" {
		t.Fatalf("configured origin response: status=%d headers=%v", configuredAllowed.Code, configuredAllowed.Header())
	}
	blocked := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(blocked, r)
	if blocked.Code != http.StatusForbidden || blocked.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("blocked origin response: status=%d headers=%v", blocked.Code, blocked.Header())
	}
}

func TestMarsCloudClientIDFromClaims(t *testing.T) {
	jwt := MarsJSON.NewJSONObject(map[string]any{"sub": "human-42"})
	if got := marsCloudClientID(jwt); got != "human-42" {
		t.Fatalf("marsCloudClientID=%q, want human-42", got)
	}
	if got := marsCloudClientID(MarsJSON.NewJSONObject(map[string]any{})); got != "" {
		t.Fatalf("missing identity returned %q", got)
	}
}

func TestConfigurableSecurityPolicy(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if err := store.ConfigureSecurity([]string{"https://ui.example"}, []string{"192.0.2.0/24"}); err != nil {
		t.Fatal(err)
	}
	if !store.isAllowedMCPOrigin("https://ui.example") || store.isAllowedMCPOrigin("http://localhost:18790") {
		t.Fatal("custom CORS allowlist was not applied")
	}
	if !store.isTrustedProxy("192.0.2.10") || store.isTrustedProxy("198.51.100.10") {
		t.Fatal("trusted proxy CIDR policy was not applied")
	}
	if err := store.ConfigureSecurity([]string{"ftp://invalid"}, nil); err == nil {
		t.Fatal("invalid origin was accepted")
	}
	if err := store.ConfigureSecurity(nil, []string{"not-a-cidr"}); err == nil {
		t.Fatal("invalid trusted proxy CIDR was accepted")
	}
}
