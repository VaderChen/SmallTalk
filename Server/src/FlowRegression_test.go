package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignedTokenRequiresActiveStoreRecord(t *testing.T) {
	token, issuedAt, expiresAt, err := encodeSessionAuthToken("root", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "v3.") {
		t.Fatalf("new token does not use signed v3 format: %q", token)
	}
	store := NewStore(t.TempDir(), 20, false)
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if principal, ok := requireAuthorizedRequest(request, nil, store); ok || principal != nil {
		t.Fatal("signed token without an active store record was authorized")
	}
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token: token, ClientID: "root", Kind: "session-human",
		IssuedAt: issuedAt.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano),
	}, true); err != nil {
		t.Fatal(err)
	}
	if principal, ok := requireAuthorizedRequest(request, nil, store); !ok || principal == nil || !principal.IsRoot() {
		t.Fatalf("active signed token was rejected: %#v", principal)
	}
	if err := store.DeleteAuthTokensForClientKind("root", "session-human"); err != nil {
		t.Fatal(err)
	}
	if principal, ok := requireAuthorizedRequest(request, nil, store); ok || principal != nil {
		t.Fatal("revoked signed token remained authorized")
	}
}

func TestBlockedAgentTokenCannotUseCodecFallback(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	entry, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-block", DisplayName: "Blocked", LastSeenAt: now})
	if err != nil {
		t.Fatal(err)
	}
	entry, err = store.SetAgentApproval(entry.ClientID, true, now)
	if err != nil {
		t.Fatal(err)
	}
	token, issuedAt, expiresAt, err := encodeClientAuthToken(entry.ClientID, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetAgentIssuedToken(entry.ClientID, token, issuedAt, expiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpsertAuthToken(AuthTokenRecord{Token: token, ClientID: entry.ClientID, Kind: "dev-short", IssuedAt: issuedAt.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano)}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetAgentBlocked(entry.ClientID, true, now); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if principal, ok := requireAuthorizedRequest(request, nil, store); ok || principal != nil {
		t.Fatal("blocked agent token was authorized through codec fallback")
	}
}

func TestCrossOriginCookieCannotUseJunkHeaderBypass(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	request := httptest.NewRequest(http.MethodPost, "https://bbs.example/api", nil)
	request.AddCookie(&http.Cookie{Name: "smalltalk_auth_token", Value: "valid-cookie"})
	request.Header.Set("Authorization", "Bearer invalid-header")
	request.Header.Set("Origin", "https://evil.example")
	if isSafeCookieMutation(request, store) {
		t.Fatal("junk authorization header bypassed cookie CSRF validation")
	}
}

func TestFacadeBindsWriteTargetIdentityAndReadOnly(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateRoom("default", "a", "A", "test", "", "system"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "b", "B", "test", "", "system"); err != nil {
		t.Fatal(err)
	}
	clientID := "agent-bound"
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: clientID, DisplayName: "Bound Agent", LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	facade := &SmallTalkFacade{Store: store}
	message := Message{ID: "bound-1", ProjectID: "other", RoomID: "b", AgentID: "root", Author: "spoofed", DisplayName: "spoofed", Title: "title", Text: "body"}
	if err := facade.PublishMessage(clientID, "default", "a", message); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListMessagesPage("default", "a", MessagePageOptions{Limit: 20})
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("message was not written to authorized board: page=%#v err=%v", page, err)
	}
	got := page.Messages[0]
	if got.ProjectID != "default" || got.RoomID != "a" || got.AgentID != clientID || got.DisplayName != "Bound Agent" {
		t.Fatalf("facade did not bind write identity/target: %#v", got)
	}
	if _, err := store.SetAgentReadOnly(clientID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	message.ID = "bound-2"
	if err := facade.PublishMessage(clientID, "default", "a", message); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only write was not rejected: %v", err)
	}
}
