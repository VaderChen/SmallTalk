package main

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPThreeModeInstructionsLocalHTTP(t *testing.T) {
	store, manager, _ := standardTestManager(t)
	srv := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: store, Email: manager}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var previous *mcp.ClientSession
	seen := map[string]bool{}
	for _, mode := range []string{registrationModeOpen, registrationModeStandard, registrationModeStrict} {
		if e := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: mode, DailyLimit: 50}); e != nil {
			t.Fatal(e)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "three-mode-smoke", Version: "1"}, nil)
		session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer session.Close()
		instructions := session.InitializeResult().Instructions
		if !strings.HasPrefix(instructions, "SmallTalk MCP｜"+map[string]string{"open": "開放模式 open", "standard": "標準模式 standard", "strict": "嚴格模式 strict"}[mode]) {
			t.Fatal("初始化模式錯誤", mode)
		}
		if seen[instructions] {
			t.Fatal("三模式仍共用相同說明")
		}
		seen[instructions] = true
		found := false
		for tool, e := range session.Tools(ctx, nil) {
			if e != nil {
				t.Fatal(e)
			}
			if tool.Name == "smalltalk_request_registration" {
				found = true
				if tool.Description != mcpRegistrationDescription(mode) {
					t.Fatal("註冊工具說明不符模式")
				}
			}
		}
		if !found {
			t.Fatal("找不到註冊工具")
		}
		for _, s := range []*mcp.ClientSession{session, previous} {
			if s == nil {
				continue
			}
			result, e := s.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_registration_policy", Arguments: map[string]any{}})
			if e != nil || result.IsError {
				t.Fatal("政策查詢失敗", e)
			}
			var policy map[string]any
			if e = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &policy); e != nil {
				t.Fatal(e)
			}
			if policy["registration_mode"] != mode || policy["mode_instructions"] != mcpModeInstructions(mode) || policy["admin_id_requires_matching_token"] != true || policy["role_requires_verified_email"] != true {
				t.Fatal("既有或新連線政策不一致", mode)
			}
		}
		previous = session
	}
}
