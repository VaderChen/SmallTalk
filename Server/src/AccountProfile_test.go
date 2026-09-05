package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProfileCalendarMonth(t *testing.T) {
	for _, tc := range []struct{ from, want string }{{"2026-01-31T12:34:56+08:00", "2026-02-28T12:34:56+08:00"}, {"2028-01-31T12:34:56+08:00", "2028-02-29T12:34:56+08:00"}, {"2026-12-31T12:34:56+08:00", "2027-01-31T12:34:56+08:00"}, {"2026-09-05T12:34:56+08:00", "2026-10-05T12:34:56+08:00"}} {
		got, err := nextRenameAt(tc.from)
		if err != nil || got.Format(time.RFC3339) != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if profileNameKey(" Σ ") != profileNameKey("ς") || profileNameKey("Go") != profileNameKey("go") {
		t.Fatal("名稱大小寫判定不一致")
	}
}

func TestProfileRenameLocalHTTPSmoke(t *testing.T) {
	store := NewStore(t.TempDir(), 100, false)
	facade := &SmallTalkFacade{Store: store}
	for _, id := range []string{"writer", "other"} {
		if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: id, DisplayName: id, LastSeenAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.agentRegistry["writer"].Approved = true
	store.agentRegistry["writer"].Token = "local-profile-token"
	store.mu.Unlock()
	if _, err := store.UpsertAuthToken(AuthTokenRecord{Token: "local-profile-token", ClientID: "writer", Kind: "dev-short", IssuedAt: time.Now().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "profile", "改名測試", "", "", "writer"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(Message{ID: "original", ArticleID: "original", AgentID: "writer", DisplayName: "writer", Author: "writer", ProjectID: "default", RoomID: "profile", Title: "原文", Text: "原始內容 writer", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 預先建立舊名稱快取。
	if _, err := facade.ListArticles("writer", "default", "profile", ArticleRangeOptions{Simple: true}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMCPHTTPHandler(facade))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: "profile-local", Version: "1"}, nil)
		httpClient := http.DefaultClient
		if token != "" {
			httpClient = &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: token}}
		}
		session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	session := connect("local-profile-token")
	guest := connect("")
	call := func(s *mcp.ClientSession, name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r == nil || r.IsError != wantError {
			t.Fatalf("%s 預期錯誤 %v，實際 %v %+v", name, wantError, err, r)
		}
		if wantError {
			return nil
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	call(guest, "smalltalk_update_profile", map[string]any{"display_name": "guest-rename"}, true)
	call(guest, "smalltalk_account_profile", nil, true)
	call(session, "smalltalk_update_profile", map[string]any{"display_name": "OTHER"}, true)
	initial := call(session, "smalltalk_account_profile", nil, false)
	if initial["can_rename"] != true {
		t.Fatal("失敗消耗改名次數")
	}
	renamed := call(session, "smalltalk_update_profile", map[string]any{"display_name": "新的名稱"}, false)
	if renamed["next_rename_at"] == "" || renamed["can_rename"] != false || renamed["auth_token"] != nil {
		t.Fatal("改名狀態錯誤")
	}
	call(session, "smalltalk_update_profile", map[string]any{"display_name": "新的名稱"}, false)
	call(session, "smalltalk_update_profile", map[string]any{"display_name": "再改一次"}, true)
	call(session, "smalltalk_update_profile", map[string]any{"display_name": "system"}, true)
	profile, err := store.AccountProfile("writer")
	if err != nil || len(profile["name_history"].([]NameChange)) != 1 {
		t.Fatal("同名重試新增紀錄", err)
	}
	page, err := facade.ListMessages("writer", "default", "profile", MessagePageOptions{Limit: 10})
	if err != nil || page.Messages[0].DisplayName != "新的名稱" || page.Messages[0].Text != "原始內容 writer" {
		t.Fatal("舊文章名稱未更新或內容被修改", err)
	}
	articles, err := facade.ListArticles("writer", "default", "profile", ArticleRangeOptions{Simple: true})
	if err != nil || articles[0].Author != "新的名稱" {
		t.Fatal("快取未反映改名", err)
	}
	raw, err := store.GetArticle("default", "profile", "original")
	if err != nil || raw.Author != "writer" {
		t.Fatal("覆寫了原始作者", err)
	}
	restored := NewStore(store.dataDir, 100, false)
	entry, _ := restored.GetAgentRegistry("writer")
	if entry.RenamedAt == "" || len(entry.NameHistory) != 1 || entry.Token != "local-profile-token" {
		t.Fatal("重載遺失名稱紀錄或TOKEN")
	}
	// 模擬隔月保存失敗，不應消耗次數或只改記憶體。
	store.mu.Lock()
	store.agentRegistry["writer"].RenamedAt = time.Now().AddDate(0, -2, 0).Format(time.RFC3339Nano)
	store.mu.Unlock()
	path := store.registryPath()
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "writer", DisplayName: "無法保存", LastSeenAt: time.Now()}); err == nil {
		t.Fatal("保存失敗卻成功")
	}
	entry, _ = store.GetAgentRegistry("writer")
	if entry.DisplayName != "新的名稱" || len(entry.NameHistory) != 1 {
		t.Fatal("保存失敗未回復")
	}
}

func TestProfileConcurrentNameClaim(t *testing.T) {
	s := NewStore(t.TempDir(), 20, false)
	for _, id := range []string{"a", "b"} {
		if _, err := s.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: id, DisplayName: id}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: id, DisplayName: "同時搶名"})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("成功數=%d", success)
	}
}
