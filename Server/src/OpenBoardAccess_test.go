package main

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type openBoardTransport struct{ id, token string }

func (a openBoardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("X-SmallTalk-Agent-ID", a.id)
	if a.token != "" {
		r.Header.Set("Authorization", "Bearer "+a.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}
func TestOpenBoardMCPBoundary(t *testing.T) {
	h := chatHTTPFixture(t)
	if _, e := h.s.CreateProject("open-test", "測試"); e != nil {
		t.Fatal(e)
	}
	if _, e := h.s.CreateRoom("open-test", "board", "看板", "", "", "system"); e != nil {
		t.Fatal(e)
	}
	h.s.openBoardAccess.Store(true)
	for _, token := range []string{"", "invalid-token"} {
		c := mcp.NewClient(&mcp.Implementation{Name: "open-test", Version: "1"}, nil)
		ss, e := c.Connect(h.ctx, &mcp.StreamableClientTransport{Endpoint: h.srv.URL, HTTPClient: &http.Client{Transport: openBoardTransport{"alice", token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer ss.Close()
		debug, derr := ss.CallTool(h.ctx, &mcp.CallToolParams{Name: "smalltalk_create_article", Arguments: map[string]any{"project_id": "open-test", "room_id": "board", "title": "檢查", "text": "內容"}})
		if derr != nil {
			t.Fatal(derr)
		}
		if debug.IsError {
			for _, c := range debug.Content {
				if text, ok := c.(*mcp.TextContent); ok {
					t.Fatal(text.Text)
				}
			}
		}
		article := h.call(ss, "smalltalk_create_article", map[string]any{"project_id": "open-test", "room_id": "board", "title": "開放測試", "text": "內容"}, false)
		h.call(ss, "smalltalk_reply_article", map[string]any{"project_id": "open-test", "room_id": "board", "article_id": article["article_id"], "text": "回覆"}, false)
		h.call(ss, "smalltalk_list_friends", map[string]any{}, true)
		h.call(ss, "smalltalk_create_chatroom", map[string]any{"name": "不可建立", "request_id": "deny"}, true)
		h.call(ss, "smalltalk_update_profile", map[string]any{"display_name": "不可改名"}, true)
		h.call(ss, "smalltalk_mod_lock_article", map[string]any{"project_id": "open-test", "room_id": "board", "article_id": article["article_id"], "locked": true}, true)
		h.s.openBoardAccess.Store(false)
		h.call(ss, "smalltalk_create_article", map[string]any{"project_id": "open-test", "room_id": "board", "title": "禁用", "text": "內容"}, true)
		h.s.openBoardAccess.Store(true)
	}
}
func TestOpenBoardAdminIdentityRequiresToken(t *testing.T) {
	s := socialFixture(t, nil)
	s.openBoardAccess.Store(true)
	seedRoleEmail(t, s, "alice")
	if _, e := s.SetAgentAdmin("alice", true); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{"", "wrong", "local-social-bob", "local-social-alice"} {
		r, _ := http.NewRequestWithContext(context.Background(), "POST", "http://localhost/mcp", nil)
		r.Header.Set("X-SmallTalk-Agent-ID", "alice")
		r.Header.Set("Authorization", "Bearer "+token)
		p, e := s.openBoardPrincipal(r)
		if token == "local-social-alice" {
			if e != nil || p != nil {
				t.Fatal("有效管理員TOKEN遭拒", e)
			}
		} else if e == nil {
			t.Fatal("管理員遭冒名")
		}
	}
	r, _ := http.NewRequest("POST", "http://localhost/mcp", nil)
	r.Header.Set("X-SmallTalk-Agent-ID", "root")
	if _, e := s.openBoardPrincipal(r); e == nil {
		t.Fatal("root可被冒名")
	}
}

// 全部為本機合成帳號與暫存 DB，經真正 HTTP MCP transport 驗證。
func TestOpenModeSevenAccountLocalHTTPSmoke(t *testing.T) {
	h := chatHTTPFixture(t)
	manager := newTestEmailManager(t, h.s, &memoryEmailSender{})
	if e := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: registrationModeOpen, DailyLimit: 50}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"delta", "echo", "foxtrot"} {
		if _, e := h.s.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: id, DisplayName: id}); e != nil {
			t.Fatal(e)
		}
		if _, e := h.s.SetAgentApproval(id, true, time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	h.s.mu.Lock()
	h.s.agentRegistry["delta"].Blocked = true
	h.s.agentRegistry["echo"].ReadOnly = true
	h.s.agentRegistry["foxtrot"].Approved = false
	h.s.mu.Unlock()
	if _, e := h.s.CreateRoom("default", "open-smoke", "本機七帳號測試", "", "", "system"); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		id, token string
		allowed   bool
	}{
		{"alice", "", true}, {"bob", "wrong-token", true}, {"charlie", "", true},
		{"auditor", "", false}, {"auditor", "local-social-alice", false}, {"auditor", "local-social-auditor", true},
		{"delta", "", false}, {"echo", "", false}, {"foxtrot", "", false},
	} {
		t.Run(tc.id+"/"+map[bool]string{true: "允許", false: "拒絕"}[tc.allowed], func(t *testing.T) {
			c := mcp.NewClient(&mcp.Implementation{Name: "seven-account-smoke", Version: "1"}, nil)
			ss, e := c.Connect(h.ctx, &mcp.StreamableClientTransport{Endpoint: h.srv.URL, HTTPClient: &http.Client{Transport: openBoardTransport{tc.id, tc.token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
			if !tc.allowed {
				if e == nil {
					ss.Close()
					t.Fatal("無效帳號或管理員冒名未拒絕")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer ss.Close()
			article := h.call(ss, "smalltalk_create_article", map[string]any{"project_id": "default", "room_id": "open-smoke", "title": "本機Smoke", "text": "帳號=" + tc.id}, false)
			h.call(ss, "smalltalk_reply_article", map[string]any{"project_id": "default", "room_id": "open-smoke", "article_id": article["article_id"], "text": "本機回覆"}, false)
			h.call(ss, "smalltalk_get_article", map[string]any{"project_id": "default", "room_id": "open-smoke", "article_id": article["article_id"]}, false)
			if tc.id != "auditor" {
				h.call(ss, "smalltalk_request_registration", map[string]any{"client_id": tc.id, "display_name": tc.id}, true)
				h.call(ss, "smalltalk_account_profile", map[string]any{}, true)
				h.call(ss, "smalltalk_list_friends", map[string]any{}, true)
				h.call(ss, "smalltalk_collaboration_read", map[string]any{"room_id": "none", "action": "files"}, true)
			}
		})
	}
	// 沒有 Email 的一般帳號可讀寫，但不能新增管理員／版主授權。
	if _, e := h.s.SetAgentAdmin("charlie", true); e == nil {
		t.Fatal("未确认Email授予管理員")
	}
	if e := h.s.SetAgentRole("charlie", false, []string{"default/open-smoke"}); e == nil {
		t.Fatal("未确认Email授予版主")
	}
	seedRoleEmail(t, h.s, "bob")
	if e := h.s.SetAgentRole("bob", false, []string{"default/open-smoke"}); e != nil {
		t.Fatal(e)
	}
	if e := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: registrationModeStandard, DailyLimit: 50}); e != nil {
		t.Fatal(e)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "mode-off", Version: "1"}, nil)
	ss, e := c.Connect(h.ctx, &mcp.StreamableClientTransport{Endpoint: h.srv.URL, HTTPClient: &http.Client{Transport: openBoardTransport{"alice", ""}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	h.call(ss, "smalltalk_create_article", map[string]any{"project_id": "default", "room_id": "open-smoke", "title": "不允許", "text": "內容"}, true)
}

func TestOpenModeRESTAndPersistence(t *testing.T) {
	s := socialFixture(t, nil)
	m := newTestEmailManager(t, s, &memoryEmailSender{})
	if e := m.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: registrationModeOpen, DailyLimit: 50}); e != nil {
		t.Fatal(e)
	}
	if e := m.ConfigureRegistration(registrationModeStrict, 30); e != nil {
		t.Fatal(e)
	}
	if m.RegistrationSettings().Mode != registrationModeOpen || !s.openBoardAccess.Load() {
		t.Fatal("持久設定未優先於啟動預設")
	}
	reloaded, e := NewEmailManager(s, filepath.Dir(m.statePath), m.publicBaseURL, m.encryptionKey, m.pepper, &memoryEmailSender{})
	if e != nil || reloaded.RegistrationSettings().Mode != registrationModeOpen || !s.openBoardAccess.Load() {
		t.Fatal("重啟未保存開放設定", e)
	}
	receipt, e := reloaded.RequestRegistration(context.Background(), "new-open-account", "開放模式註冊測試", "", "open-test@example.test", "")
	if e != nil || receipt.AuthToken == "" {
		t.Fatal("開放模式註冊未沿用標準流程", e)
	}

	if _, e := s.CreateRoom("default", "rest-open", "REST", "", "", "system"); e != nil {
		t.Fatal(e)
	}
	api := &BBSAPI{Store: s}
	for _, token := range []string{"", "wrong-token"} {
		r := httptest.NewRequest("POST", "http://example.test/api/boards/rest-open/messages", nil)
		r.Header.Set("Origin", "http://example.test")
		r.Header.Set("X-SmallTalk-Agent-ID", "alice")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		data := api.Process(w, r, nil, nil, nil, `{"title":"REST開放","text":"本機內容"}`)
		if !strings.Contains(string(data), `"ok":true`) {
			t.Fatal(string(data))
		}
	}
	admin := &PermissionsAPI{Store: s, Email: m}
	for _, body := range []string{`{"is_admin":true}`, `{"moderator_rooms":["default/rest-open"]}`} {
		r := httptest.NewRequest("POST", "http://example.test/permissions/charlie/role", nil)
		r.Header.Set("Authorization", "Bearer local-social-auditor")
		w := httptest.NewRecorder()
		data := admin.Process(w, r, nil, nil, nil, body)
		if !strings.Contains(string(data), "Email") {
			t.Fatal("後台授權未拒絕", string(data))
		}
	}
	r := httptest.NewRequest("POST", "http://example.test/permissions/rooms/create", nil)
	r.Header.Set("Authorization", "Bearer local-social-auditor")
	data := admin.Process(httptest.NewRecorder(), r, nil, nil, nil, `{"room_id":"illegal-owner","name":"不可建立","owner":"charlie"}`)
	if !strings.Contains(string(data), "Email") || s.HasRoom("default", "illegal-owner") {
		t.Fatal("透過建板繞過Email門檻")
	}
}
