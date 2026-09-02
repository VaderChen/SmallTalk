package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientACLAllowDenyRules(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if err := store.UpsertClientACL("agent-a", []RoomRef{{ProjectID: "default", RoomID: "allowed"}}, []RoomRef{{ProjectID: "default", RoomID: "blocked"}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		project, room string
		want          bool
	}{
		{"default", "allowed", true},
		{"default", "blocked", false},
		{"default", "other", false},
	}
	for _, tc := range cases {
		if got := store.CanClientAccessRoom("agent-a", tc.project, tc.room); got != tc.want {
			t.Errorf("access %s/%s=%v, want %v", tc.project, tc.room, got, tc.want)
		}
	}
	if !store.CanClientAccessRoom("agent-b", "default", "other") {
		t.Fatal("client without ACL should have default access")
	}
}

func TestAuthorizeAuthTokenPrincipalAndSource(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	valid := AuthTokenRecord{Token: "token-valid", ClientID: "agent-a", Kind: "dev-short", SourceIP: "127.0.0.1", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}
	if _, err := store.UpsertAuthToken(valid, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://example.test/mcp", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer token-valid")
	principal, ok := requireAuthorizedRequest(r, nil, store)
	if !ok {
		t.Fatal("valid token was rejected")
	}
	if principal.ClientID != "agent-a" || principal.PrincipalType != "agent" || principal.TokenKind != "dev-short" {
		t.Fatalf("unexpected principal: %+v", principal)
	}

	wrongSource := r.Clone(r.Context())
	wrongSource.RemoteAddr = "192.0.2.10:1234"
	if _, ok := requireAuthorizedRequest(wrongSource, nil, store); ok {
		t.Fatal("token with wrong source IP was accepted")
	}
}

func TestHumanSessionTokenAllowsChangedSourceIP(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token:     "human-session",
		ClientID:  "root",
		Kind:      "session-human",
		SourceIP:  "192.0.2.10",
		IssuedAt:  now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://example.test/permissions/rooms", nil)
	r.RemoteAddr = "198.51.100.20:1234"
	r.Header.Set("Authorization", "Bearer human-session")
	principal, ok := requireAuthorizedRequest(r, nil, store)
	if !ok || principal.ClientID != "root" || principal.PrincipalType != "root" {
		t.Fatalf("human session with changed source IP was rejected: principal=%+v ok=%v", principal, ok)
	}
}

func TestExpiredAndBlockedAuthTokenRejected(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	expired := AuthTokenRecord{Token: "token-expired", ClientID: "agent-expired", Kind: "dev-short", IssuedAt: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}
	if _, err := store.UpsertAuthToken(expired, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://example.test/mcp", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer token-expired")
	if _, ok := requireAuthorizedRequest(r, nil, store); ok {
		t.Fatal("expired token was accepted")
	}
	if _, ok := store.GetAuthTokenRecord("token-expired"); ok {
		t.Fatal("expired token was not removed")
	}

	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-blocked"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentBlocked("agent-blocked", true, now); err != nil {
		t.Fatal(err)
	}
	blocked := AuthTokenRecord{Token: "token-blocked", ClientID: "agent-blocked", Kind: "dev-short", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}
	if _, err := store.UpsertAuthToken(blocked, false); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer token-blocked")
	if _, ok := requireAuthorizedRequest(r, nil, store); ok {
		t.Fatal("blocked agent token was accepted")
	}
}

func TestRootPrincipal(t *testing.T) {
	if !(&requestAuthContext{ClientID: "root", PrincipalType: "root"}).IsRoot() {
		t.Fatal("root principal was not recognized")
	}
	if (&requestAuthContext{ClientID: "agent-rootlike"}).IsRoot() {
		t.Fatal("non-root principal was recognized as root")
	}
}

func TestAuthTokenPayloadIdentityMismatchRejected(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	token, issuedAt, expiresAt, err := encodeClientAuthToken("agent-a", int(time.Hour/time.Second))
	if err != nil {
		t.Skipf("RSA key is not initialized in this test environment: %v", err)
	}
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token:     token,
		ClientID:  "agent-b",
		Kind:      "smalltalk-client",
		IssuedAt:  issuedAt.Format(time.RFC3339Nano),
		ExpiresAt: expiresAt.Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://example.test/mcp", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer "+token)
	if _, ok := requireAuthorizedRequest(r, nil, store); ok {
		t.Fatal("token payload identity mismatch was accepted")
	}
}

func TestSystemPrincipalRequiresSystemToken(t *testing.T) {
	if (&requestAuthContext{ClientID: "root", PrincipalType: "root", TokenKind: "dev-short"}).IsSystem() {
		t.Fatal("dev-short root token was treated as system token")
	}
	if !(&requestAuthContext{ClientID: "root", PrincipalType: "root", TokenKind: "system"}).IsSystem() {
		t.Fatal("system root token was not recognized")
	}
	if (&requestAuthContext{ClientID: "agent-a", PrincipalType: "root", TokenKind: "system"}).IsSystem() {
		t.Fatal("non-root system token was treated as system token")
	}
}
