package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAdminOwnArticleLocalHTTPSmoke(t *testing.T) {
	for _, soft := range []bool{true, false} {
		t.Run(fmt.Sprint(soft), func(t *testing.T) {
			store := NewStore(t.TempDir(), 100, false)
			if err := store.SetSystemPolicy(SystemPolicyConfig{SoftDeleteEnabled: soft, VisitorTTLDays: 15}); err != nil {
				t.Fatal(err)
			}
			facade := &SmallTalkFacade{Store: store}
			for _, room := range []string{"announce", "visitors", "custom"} {
				if _, err := store.CreateRoom("default", room, room, "", "", "system"); err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{"own", "other"} {
					author := "admin"
					if id == "other" {
						author = "another"
					}
					if err := store.AddMessage(Message{ID: id, ArticleID: id, ProjectID: "default", RoomID: room, AgentID: author, DisplayName: "相同顯示名稱", Title: id, Text: "原文", TS: time.Now()}); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.AddMessage(Message{ID: "reply", ArticleID: "other", ReplyToMessageID: "other", ProjectID: "default", RoomID: room, AgentID: "admin", Text: "本人回覆", TS: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			_, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "admin", DisplayName: "相同顯示名稱", LastSeenAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			store.agentRegistry["admin"].Approved = true
			store.agentRegistry["admin"].Token = "local-admin-token"
			store.mu.Unlock()
			if _, err := store.UpsertAuthToken(AuthTokenRecord{Token: "local-admin-token", ClientID: "admin", Kind: "dev-short", IssuedAt: time.Now().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}, false); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(NewMCPHTTPHandler(facade))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "local-admin-smoke", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: "local-admin-token"}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			call := func(room, id string, wantError bool) {
				t.Helper()
				r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_mod_delete_article", Arguments: map[string]any{"project_id": "default", "room_id": room, "article_id": id, "reason": "本機權限驗證"}})
				if err != nil || r == nil || r.IsError != wantError {
					t.Fatalf("%s/%s 預期錯誤=%v: %v %+v", room, id, wantError, err, r)
				}
			}
			for _, room := range []string{"announce", "visitors", "custom"} {
				call(room, "own", true)
			}
			if _, err := store.SetAgentAdmin("admin", true); err != nil {
				t.Fatal(err)
			}
			for _, room := range []string{"announce", "visitors", "custom"} {
				call(room, "other", true)
				call(room, "reply", true)
				call(room, "missing", true)
				if _, err := facade.ModeratorSetArticlePinned("admin", "相同顯示名稱", "default", room, "own", true); err != ErrForbidden {
					t.Fatal("額外取得置頂權限", err)
				}
			}
			store.mu.Lock()
			store.agentRegistry["admin"].ReadOnly = true
			store.mu.Unlock()
			call("announce", "own", true)
			store.mu.Lock()
			store.agentRegistry["admin"].ReadOnly = false
			store.mu.Unlock()
			if err := store.UpsertClientACL("admin", nil, []RoomRef{{ProjectID: "default", RoomID: "announce"}}); err != nil {
				t.Fatal(err)
			}
			call("announce", "own", true)
			if err := store.UpsertClientACL("admin", nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetAgentAdmin("admin", false); err != nil {
				t.Fatal(err)
			}
			call("announce", "own", true)
			if _, err := store.SetAgentAdmin("admin", true); err != nil {
				t.Fatal(err)
			}
			for _, room := range []string{"announce", "visitors", "custom"} {
				call(room, "own", false)
				own, err := store.GetArticle("default", room, "own")
				if soft {
					if err != nil || own.Body == "原文" {
						t.Fatal("未軟刪除", err)
					}
				} else if err != ErrMessageNotFound {
					t.Fatal("未永久刪除", err)
				}
				other, err := store.GetArticle("default", room, "other")
				if err != nil || other.Body != "原文" || len(other.Replies) != 1 {
					t.Fatal("拒絕操作變更了他人文章", err)
				}
			}
		})
	}
}
