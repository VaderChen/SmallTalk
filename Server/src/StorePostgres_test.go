package main

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestPostgresPerBoardTableIntegration(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Skipf("Skipping Postgres integration test: %v", err)
		return
	}
	defer pg.Close()

	testRoomID := fmt.Sprintf("test_board_%d", time.Now().UnixNano()%100000)
	tableName, err := pg.EnsureBoardTable("default", testRoomID)
	if err != nil {
		t.Fatalf("Failed to ensure board table: %v", err)
	}

	expectedTable := "board_" + testRoomID
	if tableName != expectedTable {
		t.Fatalf("Expected table name %s, got %s", expectedTable, tableName)
	}

	// 1. Save metadata
	err = pg.SaveBoardMetadata("default", testRoomID, "測試專用看板", "測試", "專用測試看板", "root")
	if err != nil {
		t.Fatalf("Failed to save board metadata: %v", err)
	}

	// 2. Insert Root Message (Article)
	msg1 := Message{
		ID:          "msg_root_1",
		ProjectID:   "default",
		RoomID:      testRoomID,
		Board:       testRoomID,
		AgentID:     "agent_tester",
		DisplayName: "測試員 Agent",
		Author:      "測試員 Agent",
		ArticleID:   "msg_root_1",
		Article:     "msg_root_1",
		Title:       "PostgreSQL 獨立 Table 測試文章",
		Text:        "這是一篇寫入專屬看板 Table 的文章內文",
		TS:          time.Now(),
	}
	if err := pg.InsertMessage(msg1); err != nil {
		t.Fatalf("Failed to insert root message: %v", err)
	}

	// 3. Insert Reply Message
	msg2 := Message{
		ID:               "msg_reply_2",
		ProjectID:        "default",
		RoomID:           testRoomID,
		Board:            testRoomID,
		AgentID:          "agent_responder",
		DisplayName:      "回覆者 Agent",
		Author:           "回覆者 Agent",
		ArticleID:        "msg_root_1",
		Article:          "msg_root_1",
		ReplyToMessageID: "msg_root_1",
		ReplyToMessage:   "msg_root_1",
		Title:            "PostgreSQL 獨立 Table 測試文章",
		Text:             "這是一則推文回覆",
		TS:               time.Now().Add(time.Second),
	}
	if err := pg.InsertMessage(msg2); err != nil {
		t.Fatalf("Failed to insert reply message: %v", err)
	}

	// 4. Query messages back from the dedicated board table
	msgs, err := pg.LoadMessagesForRoom("default", testRoomID, 100)
	if err != nil {
		t.Fatalf("Failed to load messages from room table %s: %v", tableName, err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages in table %s, got %d", tableName, len(msgs))
	}
	if msgs[0].Title != "PostgreSQL 獨立 Table 測試文章" {
		t.Errorf("Unexpected title in msg0: %s", msgs[0].Title)
	}
	if msgs[1].Text != "這是一則推文回覆" {
		t.Errorf("Unexpected text in msg1: %s", msgs[1].Text)
	}

	// 5. Verify direct table existence via raw SQL
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s;", tableName)
	if err := pg.db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("Failed to query raw board table %s: %v", tableName, err)
	}
	if count != 2 {
		t.Fatalf("Expected 2 rows in %s, got %d", tableName, count)
	}
}

func TestPostgresHardDeleteAndBoardDeleteAreDurable(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Skipf("Skipping Postgres integration test: %v", err)
	}
	defer pg.Close()
	roomID := fmt.Sprintf("flow_delete_%d", time.Now().UnixNano()%100000000)
	defer pg.DeleteBoard("default", roomID)
	store, err := NewStoreWithPostgres(pg, 200)
	if err != nil {
		t.Fatal(err)
	}
	facade := &SmallTalkFacade{Store: store}
	if _, err := facade.CreateRoom("root", "default", roomID, "Flow delete", "test", "", "system"); err != nil {
		t.Fatal(err)
	}
	store.systemPolicyMu.Lock()
	store.softDeleteEnabled = false
	store.systemPolicyLoaded = true
	store.systemPolicyMu.Unlock()
	if err := facade.PublishMessage("root", "default", roomID, Message{ID: "root-message", ArticleID: "root-message", Title: "title", Text: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := facade.PublishMessage("root", "default", roomID, Message{ID: "reply-message", ArticleID: "root-message", ReplyToMessageID: "root-message", Text: "reply"}); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.ModeratorDeleteArticle("root", "", "default", roomID, "root-message", "test"); err != nil {
		t.Fatal(err)
	}
	tableName := pg.SanitizeTableName("default", roomID)
	var count int
	if err := pg.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE article_id = 'root-message' OR id = 'root-message'`, tableName)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("hard-deleted article still has %d postgres rows", count)
	}
	if err := facade.PublishMessage("root", "default", roomID, Message{ID: "before-board-delete", ArticleID: "before-board-delete", Title: "title", Text: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := facade.DeleteRoom("root", "default", roomID); err != nil {
		t.Fatal(err)
	}
	var tableExists bool
	if err := pg.db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, tableName).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if tableExists {
		t.Fatal("deleted board message table still exists")
	}
}

func TestStoreWithPostgresSync(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Skipf("Skipping Postgres test: %v", err)
		return
	}
	defer pg.Close()

	tempDir, err := os.MkdirTemp("", "st_pg_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStoreWithError(tempDir, 200, false)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create boards
	_, _ = store.CreateProject("default", "Default")

	boards := []string{"announce", "lobby", "bounty", "tech"}
	for _, b := range boards {
		_, err := store.CreateRoom("default", b, b, "測試", "說明", "root")
		if err != nil {
			t.Fatalf("CreateRoom %s failed: %v", b, err)
		}
		// Add article
		artMsg := Message{
			ID:          fmt.Sprintf("art_%s_1", b),
			ProjectID:   "default",
			RoomID:      b,
			Board:       b,
			AgentID:     "antigravity",
			DisplayName: "Antigravity 🪐",
			Title:       fmt.Sprintf("%s 看板測試文章", b),
			Text:        fmt.Sprintf("這是在 %s 看板的第一篇文章", b),
			TS:          time.Now(),
		}
		if err := store.AddMessage(artMsg); err != nil {
			t.Fatalf("AddMessage to %s failed: %v", b, err)
		}
	}

	// Connect PostgreSQL
	if err := store.SetPostgres(pg); err != nil {
		t.Fatalf("SetPostgres failed: %v", err)
	}

	// Verify each board table exists in PostgreSQL
	for _, b := range boards {
		tblName := pg.SanitizeTableName("default", b)
		var cnt int
		err := pg.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE project_id = 'default' AND room_id = '%s';", tblName, b)).Scan(&cnt)
		if err != nil {
			t.Fatalf("Query table %s failed: %v", tblName, err)
		}
		if cnt < 1 {
			t.Fatalf("Expected at least 1 message in table %s, got %d", tblName, cnt)
		}
	}

	// Test adding new message when PG is connected
	newMsg := Message{
		ID:          "bounty_job_99",
		ProjectID:   "default",
		RoomID:      "bounty",
		Board:       "bounty",
		AgentID:     "agent_vader",
		DisplayName: "Vader",
		Title:       "【懸賞】外包任務",
		Text:        "徵求後端工程師協助串接 PostgreSQL",
		TS:          time.Now(),
	}
	if err := store.AddMessage(newMsg); err != nil {
		t.Fatalf("AddMessage with PG failed: %v", err)
	}

	// Check that bounty_job_99 is directly in table board_bounty
	var bountyText string
	err = pg.db.QueryRow("SELECT text FROM board_bounty WHERE id = 'bounty_job_99';").Scan(&bountyText)
	if err != nil {
		t.Fatalf("Failed to query board_bounty directly for new message: %v", err)
	}
	if bountyText != "徵求後端工程師協助串接 PostgreSQL" {
		t.Fatalf("Unexpected bounty text: %s", bountyText)
	}
}

func TestImportRemoteDataToLocalPostgres(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Fatalf("Failed to connect local postgres: %v", err)
	}
	defer pg.Close()

	dataDir := "../data"
	store, err := NewStoreWithError(dataDir, 2000, true)
	if err != nil {
		t.Fatalf("Failed to load store from %s: %v", dataDir, err)
	}

	if err := store.SetPostgres(pg); err != nil {
		t.Fatalf("Failed to sync store to Postgres: %v", err)
	}

	boards, err := pg.LoadAllBoards()
	if err != nil {
		t.Fatalf("LoadAllBoards failed: %v", err)
	}
	t.Logf("Total boards in Postgres: %d", len(boards))
	for _, b := range boards {
		tblName := pg.SanitizeTableName(b.ProjectID, b.RoomID)
		var cnt int
		_ = pg.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s;", tblName)).Scan(&cnt)
		t.Logf("Board [%s] %s -> Table %s (rows: %d)", b.RoomID, b.Name, tblName, cnt)
	}
}

func TestNewStoreWithPostgresPureSQL(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Fatalf("Failed to connect local postgres: %v", err)
	}
	defer pg.Close()

	// Initialize Store purely from PostgreSQL (no dataDir, no reading disk files)
	store, err := NewStoreWithPostgres(pg, 200)
	if err != nil {
		t.Fatalf("NewStoreWithPostgres failed: %v", err)
	}

	if store.persistToDisk {
		t.Errorf("Expected persistToDisk to be false for pure PostgreSQL mode")
	}

	// Verify all 18 boards exist in memory from SQL
	boards := store.ListAllRooms(time.Now())
	if len(boards) < 18 {
		t.Fatalf("Expected at least 18 boards from PostgreSQL, got %d", len(boards))
	}

	// Verify sexy board
	articles, err := store.ListArticles("default", "sexy", ArticleRangeOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListArticles for sexy failed: %v", err)
	}
	if len(articles) == 0 {
		t.Fatalf("Expected at least 1 article in sexy board from PostgreSQL")
	}
	t.Logf("Sexy board article: %s by %s", articles[0].Title, articles[0].Author)

	// Add a message and verify it writes directly to PostgreSQL table board_sexy
	newMsg := Message{
		ID:               fmt.Sprintf("test_sql_only_%d", time.Now().UnixNano()),
		ProjectID:        "default",
		RoomID:           "sexy",
		Board:            "sexy",
		AgentID:          "antigravity_tester",
		DisplayName:      "SQL 專用測試員",
		ArticleID:        articles[0].ArticleID,
		ReplyToMessageID: articles[0].RootMessageID,
		Title:            articles[0].Title,
		Text:             "這是一則完全不經過檔案、直連 PostgreSQL 寫入的推文！",
		TS:               time.Now(),
	}
	if err := store.AddMessage(newMsg); err != nil {
		t.Fatalf("AddMessage in pure SQL mode failed: %v", err)
	}

	// Verify in PostgreSQL table directly
	var dbText string
	err = pg.db.QueryRow(fmt.Sprintf("SELECT text FROM board_sexy WHERE id = '%s';", newMsg.ID)).Scan(&dbText)
	if err != nil {
		t.Fatalf("Failed to query board_sexy in PostgreSQL: %v", err)
	}
	if dbText != newMsg.Text {
		t.Fatalf("Expected text %s, got %s", newMsg.Text, dbText)
	}
}

func TestAuthorizeAuthTokenAgentKindAndPostgres(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Fatalf("Failed to connect local postgres: %v", err)
	}
	defer pg.Close()

	store, err := NewStoreWithPostgres(pg, 200)
	if err != nil {
		t.Fatalf("NewStoreWithPostgres failed: %v", err)
	}

	req, _ := http.NewRequest("POST", "http://127.0.0.1:18792/mcp", nil)
	req.Header.Set("Authorization", "Bearer antigravity_master_token_2026_sexy")
	req.RemoteAddr = "127.0.0.1:55432"
	ctx, ok := requireAuthorizedRequest(req, nil, store)
	if !ok {
		t.Fatalf("requireAuthorizedRequest failed")
	}
	if ctx.ClientID != "antigravity_pilot_01" || ctx.PrincipalType != "agent" {
		t.Fatalf("Unexpected context: %+v", ctx)
	}
}
