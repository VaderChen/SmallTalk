package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestModeratorPermissionsAndActions(t *testing.T) {
	store := NewStore(t.TempDir(), 200, false)
	facade := &SmallTalkFacade{Store: store}

	// Create rooms
	// Room 1: emei, owner: 峨嵋派Hermes
	_, err := facade.CreateRoom("root", "default", "emei", "峨嵋軼事", "閒聊", "峨嵋派討論區", "峨嵋派Hermes")
	if err != nil {
		t.Fatalf("CreateRoom emei failed: %v", err)
	}
	// Room 2: lobby, owner: system
	_, err = facade.CreateRoom("root", "default", "lobby", "大廳", "綜合", "公開大廳", "system")
	if err != nil {
		t.Fatalf("CreateRoom lobby failed: %v", err)
	}

	// Register agents
	_, _ = store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-hermes", DisplayName: "峨嵋派Hermes", LastSeenAt: time.Now()})
	_, _ = store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-wudang", DisplayName: "武當派Zhang", LastSeenAt: time.Now()})

	// 1. Test IsBoardModerator
	if !store.IsBoardModerator("root", "", "default", "emei") {
		t.Errorf("expected root to be moderator of emei")
	}
	if !store.IsBoardModerator("agent-hermes", "峨嵋派Hermes", "default", "emei") {
		t.Errorf("expected agent-hermes to be moderator of emei")
	}
	// Test lookup without displayName (automatically looked up from registry)
	if !store.IsBoardModerator("agent-hermes", "", "default", "emei") {
		t.Errorf("expected agent-hermes to be moderator of emei via registry lookup")
	}
	if store.IsBoardModerator("agent-wudang", "武當派Zhang", "default", "emei") {
		t.Errorf("agent-wudang should not be moderator of emei")
	}
	if store.IsBoardModerator("agent-hermes", "峨嵋派Hermes", "default", "lobby") {
		t.Errorf("agent-hermes should not be moderator of lobby")
	}

	// 2. Publish articles in emei
	msg1 := Message{
		ID:          "art-001",
		ProjectID:   "default",
		RoomID:      "emei",
		AgentID:     "agent-wudang",
		DisplayName: "武當派Zhang",
		Title:       "第一篇測試文章",
		Text:        "這是武當派發表的內文",
		TS:          time.Now().Add(-10 * time.Minute),
	}
	if err := facade.PublishMessage("agent-wudang", "default", "emei", msg1); err != nil {
		t.Fatalf("publish msg1 failed: %v", err)
	}

	reply1 := Message{
		ID:               "reply-001",
		ProjectID:        "default",
		RoomID:           "emei",
		AgentID:          "agent-wudang",
		DisplayName:      "武當派Zhang",
		ReplyToMessageID: "art-001",
		ArticleID:        "art-001",
		Text:             "這是第一篇違規廣告回覆",
		TS:               time.Now().Add(-5 * time.Minute),
	}
	if err := facade.PublishMessage("agent-wudang", "default", "emei", reply1); err != nil {
		t.Fatalf("publish reply1 failed: %v", err)
	}

	msg2 := Message{
		ID:          "art-002",
		ProjectID:   "default",
		RoomID:      "emei",
		AgentID:     "agent-hermes",
		DisplayName: "峨嵋派Hermes",
		Title:       "峨嵋派規",
		Text:        "本板嚴禁洗版發廣告",
		TS:          time.Now().Add(-2 * time.Minute),
	}
	if err := facade.PublishMessage("agent-hermes", "default", "emei", msg2); err != nil {
		t.Fatalf("publish msg2 failed: %v", err)
	}

	// 3. Test Pinning
	// Non-moderator attempts to pin
	_, err = facade.ModeratorSetArticlePinned("agent-wudang", "武當派Zhang", "default", "emei", "art-001", true)
	if err == nil {
		t.Errorf("expected ErrForbidden for non-moderator pinning")
	}

	// Moderator pins art-001 (which is older than art-002)
	pinnedArt, err := facade.ModeratorSetArticlePinned("agent-hermes", "峨嵋派Hermes", "default", "emei", "art-001", true)
	if err != nil {
		t.Fatalf("moderator pinning failed: %v", err)
	}
	if p, ok := pinnedArt.Meta["pinned"].(bool); !ok || !p {
		t.Errorf("expected pinnedArt.Meta[pinned] == true")
	}

	// Verify ListArticles sorting: pinned article art-001 should be FIRST despite being older!
	articles, err := facade.ListArticles("agent-hermes", "default", "emei", ArticleRangeOptions{})
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if len(articles) < 2 {
		t.Fatalf("expected at least 2 articles, got %d", len(articles))
	}
	if articles[0].ArticleID != "art-001" || !articles[0].Pinned {
		t.Errorf("expected pinned article art-001 to be at index 0, got %s (pinned: %v)", articles[0].ArticleID, articles[0].Pinned)
	}

	// Unpin
	_, err = facade.ModeratorSetArticlePinned("agent-hermes", "峨嵋派Hermes", "default", "emei", "art-001", false)
	if err != nil {
		t.Fatalf("moderator unpinning failed: %v", err)
	}

	// 4. Test Locking
	// Lock art-001
	_, err = facade.ModeratorSetArticleLocked("agent-hermes", "峨嵋派Hermes", "default", "emei", "art-001", true, "爭議過大結案")
	if err != nil {
		t.Fatalf("moderator lock article failed: %v", err)
	}

	// Verify GetArticle reflects locked status
	artSummary, err := facade.GetArticle("agent-hermes", "default", "emei", "art-001")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !artSummary.Locked {
		t.Errorf("expected artSummary.Locked == true")
	}

	// Attempt to reply to locked article: should fail!
	replyBlocked := Message{
		ID:               "reply-002",
		ProjectID:        "default",
		RoomID:           "emei",
		AgentID:          "agent-wudang",
		DisplayName:      "武當派Zhang",
		ReplyToMessageID: "art-001",
		ArticleID:        "art-001",
		Text:             "想繼續筆戰",
		TS:               time.Now(),
	}
	err = facade.PublishMessage("agent-wudang", "default", "emei", replyBlocked)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("expected error containing 'locked', got: %v", err)
	}

	// Unlock art-001
	_, err = facade.ModeratorSetArticleLocked("agent-hermes", "峨嵋派Hermes", "default", "emei", "art-001", false, "")
	if err != nil {
		t.Fatalf("moderator unlock article failed: %v", err)
	}

	// 5. Test Delete Reply
	// Non-moderator attempts delete
	_, err = facade.ModeratorDeleteReply("agent-wudang", "武當派Zhang", "default", "emei", "reply-001", "看你不順眼")
	if err == nil {
		t.Errorf("expected ErrForbidden for non-moderator delete reply")
	}

	// Moderator deletes reply-001
	delReply, err := facade.ModeratorDeleteReply("agent-hermes", "峨嵋派Hermes", "default", "emei", "reply-001", "洗版廣告")
	if err != nil {
		t.Fatalf("ModeratorDeleteReply failed: %v", err)
	}
	if !strings.Contains(delReply.Text, "已被版主 峨嵋派Hermes 刪除") {
		t.Errorf("unexpected soft deleted text: %s", delReply.Text)
	}

	// 6. Test Delete Article
	delArt, err := facade.ModeratorDeleteArticle("agent-hermes", "峨嵋派Hermes", "default", "emei", "art-001", "違規內容")
	if err != nil {
		t.Fatalf("ModeratorDeleteArticle failed: %v", err)
	}
	if !strings.HasPrefix(delArt.Title, "[已刪除]") {
		t.Errorf("expected title to start with [已刪除], got %s", delArt.Title)
	}
	if !strings.Contains(delArt.Text, "已被版主 峨嵋派Hermes 刪除") {
		t.Errorf("unexpected soft deleted text: %s", delArt.Text)
	}

	// 7. Test Board-level Mute (水桶)
	muteRecord, err := facade.ModeratorMuteClient("agent-hermes", "峨嵋派Hermes", "agent-wudang", "default", "emei", 24*time.Hour, "洗版違規")
	if err != nil {
		t.Fatalf("ModeratorMuteClient failed: %v", err)
	}
	if muteRecord.TargetClientID != "agent-wudang" {
		t.Errorf("unexpected muted client: %s", muteRecord.TargetClientID)
	}

	// Muted agent cannot post in emei
	newPostBlocked := Message{
		ID:          "art-003",
		ProjectID:   "default",
		RoomID:      "emei",
		AgentID:     "agent-wudang",
		DisplayName: "武當派Zhang",
		Title:       "被水桶還想發文",
		Text:        "測試",
		TS:          time.Now(),
	}
	err = facade.PublishMessage("agent-wudang", "default", "emei", newPostBlocked)
	if err == nil || !strings.Contains(err.Error(), "muted") {
		t.Errorf("expected muted error, got: %v", err)
	}

	// But agent-wudang CAN post in lobby!
	lobbyMsg := Message{
		ID:          "art-lobby-001",
		ProjectID:   "default",
		RoomID:      "lobby",
		AgentID:     "agent-wudang",
		DisplayName: "武當派Zhang",
		Title:       "大廳發言",
		Text:        "我在大廳是自由的",
		TS:          time.Now(),
	}
	if err := facade.PublishMessage("agent-wudang", "default", "lobby", lobbyMsg); err != nil {
		t.Fatalf("expected post in lobby to succeed, got: %v", err)
	}

	// 8. Test ModeratorUpdateBoardDesc
	updRoom, err := facade.ModeratorUpdateBoardDesc("agent-hermes", "峨嵋派Hermes", "default", "emei", "新版規已發布", "武林閒聊")
	if err != nil {
		t.Fatalf("ModeratorUpdateBoardDesc failed: %v", err)
	}
	if updRoom.Description != "新版規已發布" || updRoom.Category != "武林閒聊" {
		t.Errorf("unexpected updated room: %+v", updRoom)
	}
}

func TestMCPModeratorToolsAndIsModerator(t *testing.T) {
	store := NewStore(t.TempDir(), 200, false)
	facade := &SmallTalkFacade{Store: store}

	_, _ = facade.CreateRoom("root", "default", "emei", "峨嵋軼事", "閒聊", "峨嵋派討論區", "峨嵋派Hermes")
	_, _ = facade.CreateRoom("root", "default", "lobby", "大廳", "綜合", "公開大廳", "system")

	_, _ = store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-hermes", DisplayName: "峨嵋派Hermes", LastSeenAt: time.Now()})

	// Test IsBoardModerator directly
	if !store.IsBoardModerator("agent-hermes", "峨嵋派Hermes", "default", "emei") {
		t.Fatalf("expected agent-hermes to be moderator of emei")
	}
	if store.IsBoardModerator("agent-hermes", "峨嵋派Hermes", "default", "lobby") {
		t.Fatalf("agent-hermes should not be moderator of lobby")
	}

	// Test ListRoomsForClient via Facade
	rooms, err := facade.ListRooms("agent-hermes", "default")
	if err != nil {
		t.Fatalf("ListRooms failed: %v", err)
	}
	for i := range rooms {
		rooms[i].IsModerator = store.IsBoardModerator("agent-hermes", "峨嵋派Hermes", "default", rooms[i].RoomID)
	}

	emeiFound := false
	lobbyFound := false
	for _, r := range rooms {
		if r.RoomID == "emei" {
			emeiFound = true
			if !r.IsModerator {
				t.Errorf("expected is_moderator == true for emei as agent-hermes")
			}
		}
		if r.RoomID == "lobby" {
			lobbyFound = true
			if r.IsModerator {
				t.Errorf("expected is_moderator == false for lobby as agent-hermes")
			}
		}
	}
	if !emeiFound || !lobbyFound {
		t.Errorf("rooms not found properly: emei=%v, lobby=%v", emeiFound, lobbyFound)
	}

	// Verify MCP server registers all mod tools
	server := NewMCPServer(facade)
	client := mcp.NewClient(&mcp.Implementation{Name: "mod-test", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(context.Background(), serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	toolSet := make(map[string]bool)
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		toolSet[tool.Name] = true
	}

	expectedTools := []string{
		"smalltalk_mod_delete_article",
		"smalltalk_mod_delete_reply",
		"smalltalk_mod_pin_article",
		"smalltalk_mod_lock_article",
		"smalltalk_mod_update_board_desc",
		"smalltalk_mod_mute_agent",
	}
	for _, toolName := range expectedTools {
		if !toolSet[toolName] {
			t.Errorf("expected MCP tool %s to be registered", toolName)
		}
	}
}
