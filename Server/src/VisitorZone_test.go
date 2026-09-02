package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestVisitorZoneGuestPostingAndPermissions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithError(dir, 100, true)
	if err != nil {
		t.Fatalf("NewStoreWithError: %v", err)
	}
	ensureDefaultLobby(store)

	facade := &SmallTalkFacade{Store: store}
	api := &BBSAPI{Store: store, Facade: facade}

	// 1. Guest post to "visitors" room should SUCCEED
	reqBody := `{"title":"訪客好","text":"這是訪客的第一則留言","author":"小明"}`
	req := httptest.NewRequest("POST", "/api/boards/visitors/messages", strings.NewReader(reqBody))
	w := httptest.NewRecorder()
	respBytes := api.Process(w, req, nil, []string{"boards", "visitors", "messages"}, nil, reqBody)

	var resp map[string]any
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal resp: %v, raw: %s", err, string(respBytes))
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected guest post in visitors room to succeed, got: %s", string(respBytes))
	}

	// Verify the message in store
	msgs, err := store.ListMessages("default", "visitors", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("expected 1 message in visitors room, got %d (err=%v)", len(msgs), err)
	}
	if msgs[0].Author != "小明" || msgs[0].Title != "訪客好" {
		t.Fatalf("unexpected message content: %+v", msgs[0])
	}

	// 2. Guest post to "lobby" room should FAIL with unauthorized
	lobbyReqBody := `{"title":"駭入大廳","text":"不應該成功"}`
	lobbyReq := httptest.NewRequest("POST", "/api/boards/lobby/messages", strings.NewReader(lobbyReqBody))
	w2 := httptest.NewRecorder()
	respLobby := api.Process(w2, lobbyReq, nil, []string{"boards", "lobby", "messages"}, nil, lobbyReqBody)
	var respLobbyJSON map[string]any
	_ = json.Unmarshal(respLobby, &respLobbyJSON)
	if errMsg, _ := respLobbyJSON["error"].(string); errMsg != "unauthorized" {
		t.Fatalf("expected unauthorized for guest posting to lobby, got: %s", string(respLobby))
	}

	// 3. Guest reply in "visitors" room should FAIL (visitors can only post new root articles)
	replyBody := `{"title":"回文","text":"訪客回文","article_id":"` + msgs[0].ID + `","reply_to_message_id":"` + msgs[0].ID + `"}`
	replyReq := httptest.NewRequest("POST", "/api/boards/visitors/messages", strings.NewReader(replyBody))
	w3 := httptest.NewRecorder()
	respReply := api.Process(w3, replyReq, nil, []string{"boards", "visitors", "messages"}, nil, replyBody)
	var respReplyJSON map[string]any
	_ = json.Unmarshal(respReply, &respReplyJSON)
	if errMsg, _ := respReplyJSON["error"].(string); !strings.Contains(errMsg, "visitors can only post new articles") {
		t.Fatalf("expected reply restriction error, got: %s", string(respReply))
	}
}

func TestVisitorZone15DaysTTLPruner(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithError(dir, 100, true)
	if err != nil {
		t.Fatalf("NewStoreWithError: %v", err)
	}
	ensureDefaultLobby(store)

	now := time.Now()
	// Insert a 20-day old message
	oldMsg := Message{
		ID:        "msg-old",
		AgentID:   "Guest",
		ProjectID: "default",
		RoomID:    "visitors",
		ArticleID: "msg-old",
		Title:     "20天前的舊文",
		Text:      "應該被清除",
		TS:        now.Add(-20 * 24 * time.Hour),
	}
	// Insert a 5-day old message
	recentMsg := Message{
		ID:        "msg-recent",
		AgentID:   "Guest",
		ProjectID: "default",
		RoomID:    "visitors",
		ArticleID: "msg-recent",
		Title:     "5天前的新文",
		Text:      "應該保留",
		TS:        now.Add(-5 * 24 * time.Hour),
	}

	if err := store.AddMessage(oldMsg); err != nil {
		t.Fatalf("add old msg: %v", err)
	}
	if err := store.AddMessage(recentMsg); err != nil {
		t.Fatalf("add recent msg: %v", err)
	}

	// Verify both messages exist
	msgs, err := store.ListMessages("default", "visitors", 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("expected 2 messages before pruning, got %d", len(msgs))
	}

	// Prune messages older than 15 days
	pruned, err := store.PruneVisitorMessages(15 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("PruneVisitorMessages: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 message pruned, got %d", pruned)
	}

	// Verify in-memory messages
	msgsAfter, err := store.ListMessages("default", "visitors", 10)
	if err != nil || len(msgsAfter) != 1 {
		t.Fatalf("expected 1 message after pruning, got %d", len(msgsAfter))
	}
	if msgsAfter[0].ID != "msg-recent" {
		t.Fatalf("expected msg-recent retained, got: %s", msgsAfter[0].ID)
	}

	// Reload from disk to verify persisted messages.jsonl
	storeReloaded, err := NewStoreWithError(dir, 100, true)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	ensureDefaultLobby(storeReloaded)
	msgsReloaded, err := storeReloaded.ListMessages("default", "visitors", 10)
	if err != nil || len(msgsReloaded) != 1 {
		t.Fatalf("expected 1 message in reloaded store, got %d", len(msgsReloaded))
	}
	if msgsReloaded[0].ID != "msg-recent" {
		t.Fatalf("expected msg-recent in reloaded store, got: %s", msgsReloaded[0].ID)
	}
}

func TestMCPPostVisitorMessageTool(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithError(dir, 100, true)
	if err != nil {
		t.Fatalf("NewStoreWithError: %v", err)
	}
	ensureDefaultLobby(store)

	facade := &SmallTalkFacade{Store: store}
	server := NewMCPServer(facade)
	client := mcp.NewClient(&mcp.Implementation{Name: "visitor-test", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	go server.Connect(context.Background(), serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect mcp: %v", err)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Call smalltalk_post_visitor_message without any authentication token
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_post_visitor_message",
		Arguments: map[string]any{
			"title":  "來自 MCP 的訪客訊息",
			"text":   "免 Token 即可留言於訪客專區",
			"author": "MCP Agent 訪客",
		},
	})
	if err != nil {
		t.Fatalf("call smalltalk_post_visitor_message tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected tool success, got error: %v", res.Content)
	}

	// Verify message in visitors room
	msgs, err := store.ListMessages("default", "visitors", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("expected 1 message in visitors room, got %d", len(msgs))
	}
	if msgs[0].Author != "MCP Agent 訪客" || msgs[0].Title != "來自 MCP 的訪客訊息" {
		t.Fatalf("unexpected message content: %+v", msgs[0])
	}
}
