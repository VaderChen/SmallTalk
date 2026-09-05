package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSocialHTTPRoleBoundaries(t *testing.T) {
	s := socialFixture(t, nil)
	socialBefriend(t, s, "alice", "bob")
	if _, err := s.SendPrivateMessage("alice", "bob", "僅雙方可讀原文", "role-one"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: s}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func(id string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: "social-security", Version: "1"}, nil)
		ss, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: "local-social-" + id}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		return ss
	}
	a, c, admin := connect("alice"), connect("charlie"), connect("auditor")
	call := func(ss *mcp.ClientSession, name string, args map[string]any, denied bool) map[string]any {
		t.Helper()
		r, err := ss.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if denied {
			if err == nil && (r == nil || !r.IsError) {
				t.Fatalf("%s 應拒絕卻成功", name)
			}
			return nil
		}
		if err != nil || r == nil || r.IsError {
			t.Fatalf("%s: %v %+v", name, err, r)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	page := call(c, "smalltalk_read_private_messages", map[string]any{"peer_id": "alice"}, false)
	if len(page["messages"].([]any)) != 0 {
		t.Fatal("第三方讀到私訊")
	}
	own := call(a, "smalltalk_read_private_messages", map[string]any{"peer_id": "bob"}, false)
	cursor := own["messages"].([]any)[0].(map[string]any)["id"]
	call(c, "smalltalk_read_private_messages", map[string]any{"peer_id": "alice", "before_id": cursor}, true)
	query := map[string]any{"account_id": "alice", "peer_id": "bob", "reason": "本機權限調閱測試"}
	call(c, "smalltalk_admin_audit_private_messages", query, true)
	if p := call(admin, "smalltalk_admin_audit_private_messages", query, false); p["audit_event_id"] == nil {
		t.Fatal("調閱無紀錄")
	}
	s.mu.Lock()
	s.agentRegistry["auditor"].IsAdmin = false
	s.agentRegistry["alice"].ReadOnly = true
	s.mu.Unlock()
	// 沿用降權之前的同一條 session，不能靠初始化時的管理權限繼續調閱。
	call(admin, "smalltalk_admin_audit_private_messages", query, true)
	if p := call(a, "smalltalk_read_private_messages", map[string]any{"peer_id": "bob"}, false); len(p["messages"].([]any)) != 1 {
		t.Fatal("唯讀 Agent 失去本人的閱讀權")
	}
	call(a, "smalltalk_send_private_message", map[string]any{"recipient_id": "bob", "text": "不能發送", "request_id": "readonly"}, true)
	call(a, "smalltalk_manage_friend", map[string]any{"peer_id": "bob", "action": "remove"}, true)
}
