package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOfflineDefaultLogin(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	h := &HttpAPI_auth{
		DefaultAccount:  "root",
		DefaultPassword: "root-password-123",
		Store:           store,
	}
	r := httptest.NewRequest("POST", "http://example.test/auth/login", strings.NewReader(`{"account":"root","password":"root-password-123"}`))
	w := httptest.NewRecorder()
	resp := h.handleLogin(w, r, `{"account":"root","password":"root-password-123"}`, true)

	var got AuthLoginResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !got.OK || got.Account != "root" || got.Project != defaultLobbyProjectID || got.AuthToken != "" {
		t.Fatalf("unexpected offline login response: %+v", got)
	}
	if _, err := r.Cookie("smalltalk_auth_token"); err == nil {
		t.Fatal("request unexpectedly contained auth cookie")
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("offline login did not set cookies")
	}
	var sessionToken string
	for _, cookie := range cookies {
		if cookie.Name == "smalltalk_auth_token" {
			sessionToken = cookie.Value
			if !cookie.HttpOnly {
				t.Fatal("authentication cookie must be HttpOnly")
			}
		}
	}
	if record, ok := store.GetAuthTokenRecord(sessionToken); !ok || record.ClientID != "root" || record.Kind != "session-human" {
		t.Fatalf("offline session was not persisted: record=%+v ok=%v", record, ok)
	}
}

func TestOfflineDefaultLoginRejectsWrongCredentials(t *testing.T) {
	h := &HttpAPI_auth{DefaultAccount: "root", DefaultPassword: "root-password-123"}
	r := httptest.NewRequest("POST", "http://example.test/auth/login", nil)
	w := httptest.NewRecorder()
	resp := h.handleLogin(w, r, `{"account":"root","password":"wrong"}`, true)

	var got ErrorResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got.Error != "login failed" {
		t.Fatalf("unexpected wrong-credential response: %+v", got)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("wrong credentials set authentication cookies")
	}
}
