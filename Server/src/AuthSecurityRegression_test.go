package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
)

func TestSourceIPIgnoresUntrustedForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	if got := sourceIPOf(r); got != "192.0.2.10" {
		t.Fatalf("sourceIPOf=%q, want TCP peer IP", got)
	}
}

func TestMCPOriginAllowlist(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	h := NewMCPHTTPHandler(&SmallTalkFacade{Store: store})

	allowed := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "http://localhost:18790")
	h.ServeHTTP(allowed, r)
	if allowed.Code != http.StatusNoContent || allowed.Header().Get("Access-Control-Allow-Origin") != "http://localhost:18790" {
		t.Fatalf("allowed origin response: status=%d headers=%v", allowed.Code, allowed.Header())
	}

	defaultOpen := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(defaultOpen, r)
	if defaultOpen.Code != http.StatusNoContent || defaultOpen.Header().Get("Access-Control-Allow-Origin") != "https://evil.example" {
		t.Fatalf("default-open origin response: status=%d headers=%v", defaultOpen.Code, defaultOpen.Header())
	}

	if err := store.ConfigureSecurity([]string{"http://localhost:18790"}, nil); err != nil {
		t.Fatal(err)
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
