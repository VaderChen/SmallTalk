package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func chatFixture(t *testing.T, pg *PostgresStore) *Store {
	t.Helper()
	s := socialFixture(t, pg)
	if err := s.ConfigureChatArchive(filepath.Join(t.TempDir(), "archives"), filepath.Join(t.TempDir(), "website")); err != nil {
		t.Fatal(err)
	}
	return s
}
func chatCreate(t *testing.T, s *Store) string {
	t.Helper()
	p, e := s.CreateChatroom("alice", "共同討論", "room-one")
	if e != nil {
		t.Fatal(e)
	}
	return p["room_id"].(string)
}
func chatJoin(t *testing.T, s *Store, id string) {
	t.Helper()
	socialBefriend(t, s, "alice", "bob")
	if _, e := s.ManageChatroom("alice", id, "invite", "bob"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageChatroom("bob", id, "accept", ""); e != nil {
		t.Fatal(e)
	}
}

type chatHTTP struct {
	t   *testing.T
	s   *Store
	ctx context.Context
	srv *httptest.Server
}

func chatHTTPFixture(t *testing.T) *chatHTTP {
	t.Helper()
	s := chatFixture(t, nil)
	srv := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: s}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return &chatHTTP{t, s, ctx, srv}
}
func (h *chatHTTP) connect(token string) *mcp.ClientSession {
	h.t.Helper()
	hc := http.DefaultClient
	if token != "" {
		hc = &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: token}}
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "chat-smoke", Version: "1"}, nil)
	ss, e := c.Connect(h.ctx, &mcp.StreamableClientTransport{Endpoint: h.srv.URL, HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		h.t.Fatal(e)
	}
	h.t.Cleanup(func() { ss.Close() })
	return ss
}
func (h *chatHTTP) call(ss *mcp.ClientSession, name string, args map[string]any, denied bool) map[string]any {
	h.t.Helper()
	r, e := ss.CallTool(h.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if denied {
		if e == nil && (r == nil || !r.IsError) {
			h.t.Fatalf("%s 應拒絕", name)
		}
		return nil
	}
	if e != nil || r == nil || r.IsError {
		h.t.Fatalf("%s: %v %+v", name, e, r)
	}
	var p map[string]any
	if e := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &p); e != nil {
		h.t.Fatal(e)
	}
	return p
}

func TestChatSmoke01MCPFlow(t *testing.T) {
	h := chatHTTPFixture(t)
	a, b := h.connect("local-social-alice"), h.connect("local-social-bob")
	args := map[string]any{"name": "Agent 討論室", "request_id": "create-http"}
	r := h.call(a, "smalltalk_create_chatroom", args, false)
	id := r["room_id"].(string)
	if id == "" || r["name"] != "Agent 討論室" {
		t.Fatal("ID與名稱未保存")
	}
	if r := h.call(a, "smalltalk_create_chatroom", args, false); r["room_id"] != id || r["duplicate"] != true {
		t.Fatal("建立重試不一致")
	}
	second := h.call(a, "smalltalk_create_chatroom", map[string]any{"name": "Agent 討論室", "request_id": "another-room"}, false)
	if second["room_id"] == id {
		t.Fatal("同名聊天室ID衝突")
	}
	socialBefriend(t, h.s, "alice", "bob")
	h.call(a, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "invite", "peer_id": "bob"}, false)
	h.call(b, "smalltalk_read_chatroom", map[string]any{"room_id": id}, true)
	h.call(b, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "accept"}, false)
	h.call(b, "smalltalk_send_chatroom_message", map[string]any{"room_id": id, "text": "第一行\n第二行", "request_id": "send-http"}, false)
	page := h.call(a, "smalltalk_read_chatroom", map[string]any{"room_id": id}, false)
	rows := page["records"].([]any)
	last := rows[len(rows)-1].(map[string]any)
	if last["text"] != "第一行\n第二行" {
		t.Fatal("換行失真")
	}
	if r := h.call(a, "smalltalk_list_chatrooms", nil, false); len(r["chatrooms"].([]any)) != 2 {
		t.Fatal("列表缺房間")
	}
}
func TestChatSmoke02Permissions(t *testing.T) {
	h := chatHTTPFixture(t)
	id := chatCreate(t, h.s)
	a, b, c, guest := h.connect("local-social-alice"), h.connect("local-social-bob"), h.connect("local-social-charlie"), h.connect("")
	h.call(a, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "invite", "peer_id": "charlie"}, true)
	for _, ss := range []*mcp.ClientSession{b, c, guest} {
		h.call(ss, "smalltalk_read_chatroom", map[string]any{"room_id": id}, true)
		h.call(ss, "smalltalk_chatroom_archive", map[string]any{"room_id": id}, true)
	}
	chatJoin(t, h.s, id)
	h.call(b, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "close"}, true)
	h.call(b, "smalltalk_chatroom_archive", map[string]any{"room_id": id}, true)
	h.s.mu.Lock()
	h.s.agentRegistry["bob"].ReadOnly = true
	h.s.mu.Unlock()
	h.call(b, "smalltalk_read_chatroom", map[string]any{"room_id": id}, false)
	h.call(b, "smalltalk_send_chatroom_message", map[string]any{"room_id": id, "text": "拒絕", "request_id": "readonly"}, true)
	record, token, e := h.s.createViewRequest("local-chat-smoke")
	if e != nil {
		t.Fatal(e)
	}
	if e := h.s.approveViewRequest(record.ID, &requestAuthContext{ClientID: "alice", TokenKind: "dev-short", CredentialHash: viewHash("local-social-alice")}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.s.pollViewRequest(token); e != nil {
		t.Fatal(e)
	}
	human := h.connect(token)
	h.call(human, "smalltalk_list_chatrooms", nil, true)
	h.call(human, "smalltalk_read_chatroom", map[string]any{"room_id": id}, true)
	h.call(human, "smalltalk_chatroom_archive", map[string]any{"room_id": id}, true)
	h.call(a, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "remove", "peer_id": "bob"}, false)
	h.call(b, "smalltalk_read_chatroom", map[string]any{"room_id": id}, true)
}
func TestChatSmoke03ScheduledArchive(t *testing.T) {
	for _, mode := range []string{"local", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			var pg *PostgresStore
			if mode == "postgres" {
				pg = isolatedPostgresForTest(t)
			}
			s := chatFixture(t, pg)
			id := chatCreate(t, s)
			chatJoin(t, s, id)
			if _, e := s.SendChatMessage("bob", id, "JSONL 原文\n保留換行", "archive-send"); e != nil {
				t.Fatal(e)
			}
			if e := s.ExportPendingChatrooms(); e != nil {
				t.Fatal(e)
			}
			meta, e := s.ReadChatArchive("alice", id, 0, 65536)
			if e != nil {
				t.Fatal(e)
			}
			archive := meta["archive"].(ChatArchive)
			if archive.Filename != "transcript.jsonl" || archive.Directory != filepath.Join(s.chatArchiveDir, id) {
				t.Fatal("檔名或位置錯誤")
			}
			raw, e := base64.StdEncoding.DecodeString(meta["content_base64"].(string))
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != archive.SHA256 {
				t.Fatal("SHA不符")
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if int64(len(lines)) != archive.Records+1 {
				t.Fatal("JSONL筆數不符")
			}
			for _, line := range lines {
				if !json.Valid([]byte(line)) {
					t.Fatal("JSONL格式錯誤")
				}
			}
			info, e := os.Stat(filepath.Join(archive.Directory, archive.Filename))
			if e != nil || info.Mode().Perm() != 0600 {
				t.Fatal("匯出權限", e)
			}
			if pg != nil {
				var path string
				if e := pg.db.QueryRow(`SELECT payload->'archive'->>'directory' FROM agent_chatrooms WHERE id=$1`, id).Scan(&path); e != nil || path != archive.Directory {
					t.Fatal("DB未記錄位置", e)
				}
			}
			stop := StartChatArchiveWorker(s, 10*time.Millisecond, nil)
			defer stop()
			if _, e := s.SendChatMessage("alice", id, "排程後新增", "scheduled"); e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				p, e := s.ReadChatArchive("alice", id, 0, 0)
				if e != nil {
					t.Fatal(e)
				}
				if p["archive_pending"] == false {
					if p["archive"].(ChatArchive).Directory != archive.Directory {
						t.Fatal("固定路徑變更")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("排程未匯出新資料")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
func TestChatSmoke04FailureRecovery(t *testing.T) {
	pg := isolatedPostgresForTest(t)
	s := chatFixture(t, pg)
	id := chatCreate(t, s)
	chatJoin(t, s, id)
	if e := s.ExportChatroom(id); e != nil {
		t.Fatal(e)
	}
	root := s.chatArchiveDir
	parked := root + "-parked"
	if e := os.Rename(root, parked); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(root, []byte("故障注入"), 0600); e != nil {
		t.Fatal(e)
	}
	closed, e := s.ManageChatroom("alice", id, "close", "")
	if e != nil || closed["archive_pending"] != true {
		t.Fatal("匯出失敗應保持關閉待重試", e)
	}
	reload, e := NewStoreWithPostgres(pg, 100)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := reload.SendChatMessage("bob", id, "已關閉", "closed-send"); e == nil {
		t.Fatal("重啟後關閉狀態遺失")
	}
	if e := os.Remove(root); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(parked, root); e != nil {
		t.Fatal(e)
	}
	if e := reload.ConfigureChatArchive(root, filepath.Join(t.TempDir(), "public")); e != nil {
		t.Fatal(e)
	}
	if e := reload.ExportPendingChatrooms(); e != nil {
		t.Fatal(e)
	}
	p, e := reload.ReadChatArchive("alice", id, 0, 65536)
	if e != nil || p["archive_pending"] != false {
		t.Fatal("重啟後未補匯出", e)
	}
	a := p["archive"].(ChatArchive)
	if e := os.WriteFile(filepath.Join(a.Directory, a.Filename), []byte("竄改"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := reload.ReadChatArchive("alice", id, 0, 10); e == nil {
		t.Fatal("雜湊異常未拒絕")
	}
	if e := reload.ExportChatroom(id); e != nil {
		t.Fatal(e)
	}
	if _, e := pg.db.Exec(`CREATE FUNCTION chat_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'chat fault'; END $$; CREATE TRIGGER chat_fail BEFORE INSERT ON agent_chat_records FOR EACH ROW EXECUTE FUNCTION chat_fail()`); e != nil {
		t.Fatal(e)
	}
	if _, e := reload.CreateChatroom("alice", "不能部分提交", "rollback"); e == nil {
		t.Fatal("事件失败仍提交")
	}
	var count int
	if e := pg.db.QueryRow(`SELECT COUNT(*) FROM agent_chatrooms WHERE request_id='rollback'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("建立未回滾", e)
	}
}
func TestChatSmoke05CloseAndConcurrency(t *testing.T) {
	for _, mode := range []string{"local", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			var pg *PostgresStore
			if mode == "postgres" {
				pg = isolatedPostgresForTest(t)
			}
			s := chatFixture(t, pg)
			id := chatCreate(t, s)
			chatJoin(t, s, id)
			var wg sync.WaitGroup
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, e := s.SendChatMessage("bob", id, "並行同訊息", "dedup"); e != nil {
						t.Error(e)
					}
				}()
			}
			wg.Wait()
			var after int64
			messages := 0
			for {
				p, e := s.ReadChatroom("alice", id, after, 1)
				if e != nil {
					t.Fatal(e)
				}
				rows := p["records"].([]ChatRecord)
				for _, r := range rows {
					if r.Kind == "message" {
						messages++
					}
					if r.RetainUntil.Before(r.CreatedAt.AddDate(0, 6, 0)) {
						t.Fatal("留存不足")
					}
				}
				after = p["next_after_seq"].(int64)
				if p["has_more"] == false {
					break
				}
			}
			if messages != 1 {
				t.Fatal("重複訊息")
			}
			if _, e := s.SendChatMessage("bob", id, "不同內容", "dedup"); e == nil {
				t.Fatal("request_id衝突")
			}
			if _, e := s.ManageChatroom("alice", id, "close", ""); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SendChatMessage("bob", id, "關閉後新訊息", "new"); e == nil {
				t.Fatal("關閉後可發言")
			}
			if _, e := s.SendChatMessage("bob", id, "並行同訊息", "dedup"); e == nil {
				t.Fatal("關閉後成員不得以重試讀回原文")
			}
			if _, e := s.ManageChatroom("alice", id, "invite", "charlie"); e == nil {
				t.Fatal("關閉後可邀請")
			}
			if e := s.DeleteAgentRegistry("bob"); e != nil {
				t.Fatal(e)
			}
			if p, e := s.ReadChatArchive("alice", id, 0, 65536); e != nil || p["archive_pending"] != false {
				t.Fatal("刪成員後失去紀錄", e)
			}
			if hits := s.SearchMessagesForClient("alice", "並行同訊息", 100); len(hits) != 0 {
				t.Fatal("聊天室流入公開搜尋")
			}
		})
	}
}
func TestChatArchivePublicPathRejected(t *testing.T) {
	s := socialFixture(t, nil)
	public := t.TempDir()
	if e := s.ConfigureChatArchive(filepath.Join(public, "archives"), public); e == nil {
		t.Fatal("允許公開匯出")
	}
}

func TestChatSmoke04SnapshotAndMissingFile(t *testing.T) {
	s := chatFixture(t, nil)
	id := chatCreate(t, s)
	if e := s.ExportChatroom(id); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.chatArchiveDir, id, chatArchiveFilename)
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := s.ExportPendingChatrooms(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("遺失的檔案未補匯出", e)
	}
	snapshot := filepath.Join(s.dataDir, "social_private.json")
	raw, e := os.ReadFile(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	if e := json.Unmarshal(raw, &fields); e != nil {
		t.Fatal(e)
	}
	delete(fields, "chat_records")
	bad, _ := json.Marshal(fields)
	if e := os.WriteFile(snapshot, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateChatroom("alice", "不可覆盖損壞快照", "corrupt"); e == nil {
		t.Fatal("損壞聊天室快照未拒絕")
	}
	after, _ := os.ReadFile(snapshot)
	if string(after) != string(bad) {
		t.Fatal("損壞快照遭覆寫")
	}
}
