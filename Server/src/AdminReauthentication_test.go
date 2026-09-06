package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminLoginWithExistingViewCookie(t *testing.T) {
	for _, state := range []string{"approved", "expired"} {
		t.Run(state, func(t *testing.T) {
			s := NewStore(t.TempDir(), 100, false)
			parent := seedViewAgent(t, s)
			record, token, e := s.createViewRequest("127.0.0.1")
			if e != nil {
				t.Fatal(e)
			}
			if e = s.approveViewRequest(record.ID, &requestAuthContext{ClientID: "view-agent", TokenKind: "agent", CredentialHash: viewHash(parent)}); e != nil {
				t.Fatal(e)
			}
			if _, e = s.pollViewRequest(token); e != nil {
				t.Fatal(e)
			}
			if state == "expired" {
				if e = s.revokeViewToken(token); e != nil {
					t.Fatal(e)
				}
			}
			auth := &HttpAPI_auth{Store: s, DefaultAccount: "root", DefaultPassword: "local-password-only"}
			request := func(path, body, origin string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(body))
				r.AddCookie(&http.Cookie{Name: "smalltalk_auth_token", Value: token})
				r.Header.Set("Origin", origin)
				w := httptest.NewRecorder()
				w.Write(auth.Process(w, r, nil, nil, nil, ""))
				return w
			}
			for _, tc := range []struct {
				password string
				ok       bool
			}{{"wrong", false}, {"local-password-only", true}} {
				w := request("/auth/login", `{"account":"root","password":"`+tc.password+`"}`, "http://example.test")
				var result AuthLoginResponse
				if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
					t.Fatal(e)
				}
				if result.OK != tc.ok || w.Code == 403 {
					t.Fatalf("%s login status=%d success=%v", state, w.Code, result.OK)
				}
				if tc.ok {
					req := httptest.NewRequest("GET", "http://example.test/permissions", nil)
					for _, c := range w.Result().Cookies() {
						req.AddCookie(c)
					}
					if e = requireHTTPRoot(req, nil, s); e != nil {
						t.Fatal("帳密驗證成功後仍無法管理", e)
					}
				} else if len(w.Result().Cookies()) != 0 {
					t.Fatal("錯誤密碼改變Cookie")
				}
			}
			if w := request("/auth/devRegister", "{}", "http://example.test"); w.Code != 403 {
				t.Fatal("唯讀限制遭放寬")
			}
			if w := request("/auth/login", `{"account":"root","password":"local-password-only"}`, "https://other.invalid"); w.Code != 403 {
				t.Fatal("跨站登入未拒絕")
			}
		})
	}
}
