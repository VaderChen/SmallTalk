package main

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func socialFixture(t *testing.T, pg *PostgresStore) *Store {
	t.Helper()
	s := NewStore(t.TempDir(), 100, false)
	if pg != nil {
		var e error
		s, e = NewStoreWithPostgres(pg, 100)
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"alice", "bob", "charlie", "auditor"} {
		if _, e := s.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: id, DisplayName: id, LastSeenAt: time.Now()}); e != nil {
			t.Fatal(e)
		}
		s.mu.Lock()
		a := s.agentRegistry[id]
		a.Approved = true
		a.Token = "local-social-" + id
		a.IsAdmin = id == "auditor"
		copy := *a
		s.mu.Unlock()
		if pg != nil {
			if e := pg.SaveAgentRegistryEntry(&copy); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := s.UpsertAuthToken(AuthTokenRecord{Token: copy.Token, ClientID: id, Kind: "dev-short", IssuedAt: time.Now().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}, false); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func socialBefriend(t *testing.T, s *Store, a, b string) {
	t.Helper()
	if _, e := s.ManageFriend(a, b, "request"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageFriend(b, a, "accept"); e != nil {
		t.Fatal(e)
	}
}
func socialLifecycle(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.SendPrivateMessage("alice", "bob", "秘密", "one"); e == nil {
		t.Fatal("非好友可寄信")
	}
	if _, e := s.ManageFriend("alice", "bob", "request"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageFriend("alice", "bob", "accept"); e == nil {
		t.Fatal("自行接受申請")
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "秘密", "one"); e == nil {
		t.Fatal("未接受可寄信")
	}
	if _, e := s.ManageFriend("bob", "alice", "accept"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.SendPrivateMessage("alice", "bob", "秘密", "one"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	page, e := s.ReadPrivateMessages("bob", "alice", "", 10)
	if e != nil {
		t.Fatal(e)
	}
	rows := page["messages"].([]PrivateMessage)
	if len(rows) != 1 {
		t.Fatal("重試重複寄送")
	}
	m := rows[0]
	if m.RetainUntil.Before(m.CreatedAt.AddDate(0, 6, 0)) {
		t.Fatal("不足六個月")
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "不同", "one"); e == nil {
		t.Fatal("request_id衝突")
	}
	p, e := s.ReadPrivateMessages("charlie", "alice", "", 10)
	if e != nil || len(p["messages"].([]PrivateMessage)) != 0 {
		t.Fatal("第三方讀到私訊", e)
	}
	if _, e := s.ReadPrivateMessages("charlie", "alice", m.ID, 10); e == nil {
		t.Fatal("跨對話游標")
	}
	s.mu.Lock()
	s.agentRegistry["alice"].DisplayName = "改名後"
	s.mu.Unlock()
	if _, e := s.SendPrivateMessage("bob", "alice", "回覆", "two"); e != nil {
		t.Fatal(e)
	}
	p, e = s.ReadPrivateMessages("alice", "bob", "", 1)
	if e != nil || p["has_more"] != true {
		t.Fatal("分頁", e)
	}
	p, e = s.ReadPrivateMessages("alice", "bob", p["next_before_id"].(string), 1)
	if e != nil || p["messages"].([]PrivateMessage)[0].SenderName != "alice" {
		t.Fatal("名稱快照改變", e)
	}
	socialBefriend(t, s, "alice", "charlie")
	if _, e := s.SendPrivateMessage("charlie", "alice", "另一對話", "three"); e != nil {
		t.Fatal(e)
	}
	p, e = s.ListPrivateConversations("alice", "", 1)
	if e != nil || p["has_more"] != true || p["conversations"].([]map[string]any)[0]["peer_id"] != "charlie" {
		t.Fatal("對話排序", e)
	}
	p, e = s.ListPrivateConversations("alice", p["next_before_id"].(string), 1)
	if e != nil || p["conversations"].([]map[string]any)[0]["peer_id"] != "bob" {
		t.Fatal("對話分頁", e)
	}
	if _, e := s.ManageFriend("bob", "alice", "block"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "封鎖後", "four"); e == nil {
		t.Fatal("封鎖後寄送")
	}
	p, e = s.SendPrivateMessage("alice", "bob", "秘密", "one")
	if e != nil || p["duplicate"] != true {
		t.Fatal("封鎖後重試", e)
	}
	if _, e := s.ManageFriend("bob", "alice", "unblock"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "解封後", "five"); e == nil {
		t.Fatal("自動恢復好友")
	}
	p, e = s.FriendHistory("alice", "bob", "", 2)
	if e != nil || p["has_more"] != true {
		t.Fatal("歷程分頁", e)
	}
	if _, e := s.FriendHistory("alice", "bob", p["next_before_id"].(string), 2); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.agentRegistry["alice"].ReadOnly = true
	s.mu.Unlock()
	if _, e := s.ReadPrivateMessages("alice", "bob", "", 10); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "charlie", "唯讀", "six"); e == nil {
		t.Fatal("唯讀帳號寫入")
	}
	if _, e := s.AuditPrivateMessages("charlie", "alice", "bob", "", "處理具體申訴案件", 10); e == nil {
		t.Fatal("非管理員調閱")
	}
	if e := s.DeleteAgentRegistry("bob"); e != nil {
		t.Fatal(e)
	}
	p, e = s.AuditPrivateMessages("auditor", "alice", "bob", "", "處理具體申訴案件", 10)
	if e != nil || len(p["messages"].([]PrivateMessage)) != 2 || p["audit_event_id"] == "" {
		t.Fatal("刪帳後追溯失敗", e)
	}
}
func TestSocialLocalLifecycle(t *testing.T) { socialLifecycle(t, socialFixture(t, nil)) }
func TestSocialPostgresLifecycle(t *testing.T) {
	socialLifecycle(t, socialFixture(t, isolatedPostgresForTest(t)))
}
func TestSocialLocalPersistenceAndFailure(t *testing.T) {
	s := socialFixture(t, nil)
	socialBefriend(t, s, "alice", "bob")
	if _, e := s.SendPrivateMessage("alice", "bob", "保留原文", "persist"); e != nil {
		t.Fatal(e)
	}
	reload := socialFixture(t, nil)
	reload.dataDir = s.dataDir
	p, e := reload.SendPrivateMessage("alice", "bob", "保留原文", "persist")
	if e != nil || p["duplicate"] != true {
		t.Fatal("重載去重", e)
	}
	path := filepath.Join(s.dataDir, "social_private.json")
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("檔案權限", e)
	}
	if e := os.WriteFile(path, []byte("損壞資料"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := reload.SendPrivateMessage("alice", "bob", "不能覆蓋", "fail"); e == nil {
		t.Fatal("損壞未拒絕")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "損壞資料" {
		t.Fatal("覆寫資料")
	}
}
func TestSocialPostgresAtomicFailure(t *testing.T) {
	pg := isolatedPostgresForTest(t)
	s := socialFixture(t, pg)
	socialBefriend(t, s, "alice", "bob")
	_, e := pg.db.Exec(`CREATE FUNCTION social_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected audit failure'; END $$; CREATE TRIGGER social_fail BEFORE INSERT ON social_events FOR EACH ROW EXECUTE FUNCTION social_test_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "不可提交", "rollback"); e == nil {
		t.Fatal("事件失敗仍提交")
	}
	if _, e := s.ManageFriend("alice", "charlie", "request"); e == nil {
		t.Fatal("好友事件失敗仍提交")
	}
	if _, e := s.AuditPrivateMessages("auditor", "alice", "bob", "", "事件失敗應停止調閱", 10); e == nil {
		t.Fatal("未保存仍調閱")
	}
	var n int
	if e := pg.db.QueryRow(`SELECT COUNT(*) FROM private_messages`).Scan(&n); e != nil || n != 0 {
		t.Fatal("訊息未回滾", e, n)
	}
	if e := pg.db.QueryRow(`SELECT COUNT(*) FROM social_relations WHERE member_b='charlie'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("好友未回滾", e, n)
	}
}
func TestSocialLocalHTTPSmoke(t *testing.T) {
	s := socialFixture(t, nil)
	srv := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: s}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: "social-local", Version: "1"}, nil)
		hc := http.DefaultClient
		if token != "" {
			hc = &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: token}}
		}
		ss, e := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { ss.Close() })
		return ss
	}
	a, b, guest := connect("local-social-alice"), connect("local-social-bob"), connect("")
	call := func(ss *mcp.ClientSession, name string, args map[string]any, wantErr bool) map[string]any {
		t.Helper()
		r, e := ss.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || r == nil || r.IsError != wantErr {
			t.Fatalf("%s: %v %+v", name, e, r)
		}
		if wantErr {
			return nil
		}
		var p map[string]any
		if e := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &p); e != nil {
			t.Fatal(e)
		}
		return p
	}
	call(guest, "smalltalk_social_policy", nil, true)
	call(a, "smalltalk_social_policy", nil, false)
	args := map[string]any{"recipient_id": "bob", "text": "僅MCP的私訊", "request_id": "http-one"}
	call(a, "smalltalk_send_private_message", args, true)
	call(a, "smalltalk_manage_friend", map[string]any{"peer_id": "bob", "action": "request"}, false)
	call(b, "smalltalk_manage_friend", map[string]any{"peer_id": "alice", "action": "accept"}, false)
	call(a, "smalltalk_send_private_message", args, false)
	if p := call(a, "smalltalk_send_private_message", args, false); p["duplicate"] != true {
		t.Fatal("HTTP重試去重")
	}
	if p := call(b, "smalltalk_read_private_messages", map[string]any{"peer_id": "alice"}, false); len(p["messages"].([]any)) != 1 {
		t.Fatal("MCP讀取")
	}
	record, viewToken, e := s.createViewRequest("local-smoke")
	if e != nil {
		t.Fatal(e)
	}
	if e := s.approveViewRequest(record.ID, &requestAuthContext{ClientID: "alice", TokenKind: "dev-short", CredentialHash: viewHash("local-social-alice")}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.pollViewRequest(viewToken); e != nil {
		t.Fatal(e)
	}
	human := connect(viewToken)
	for _, tool := range []string{"smalltalk_social_policy", "smalltalk_list_friends", "smalltalk_list_private_conversations"} {
		call(human, tool, nil, true)
	}
	call(human, "smalltalk_read_private_messages", map[string]any{"peer_id": "bob"}, true)
	if hits := s.SearchMessagesForClient("alice", "僅MCP的私訊", 100); len(hits) != 0 {
		t.Fatal("私訊進入公開搜尋")
	}
	s.mu.Lock()
	s.agentRegistry["alice"].Token = "replaced"
	s.mu.Unlock()
	r, err := a.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_read_private_messages", Arguments: map[string]any{"peer_id": "bob"}})
	if err == nil && (r == nil || !r.IsError) {
		t.Fatal("撤銷 TOKEN 仍可讀取")
	}
}

func TestSocialQuotaAndRetention(t *testing.T) {
	s := socialFixture(t, nil)
	socialBefriend(t, s, "alice", "bob")
	now := time.Now()
	yesterday := socialDay(now).Add(-time.Second)
	if e := s.socialTransaction(true, func(tx *socialTx) error {
		// 歷史原文超過六個月仍保留；配額依發送事件時間計算。
		m := PrivateMessage{ID: "old-message", Sender: "alice", Recipient: "bob", Text: "七個月前原文", RequestID: "old", CreatedAt: now.AddDate(0, -7, 0), RetainUntil: now.AddDate(0, -1, 0)}
		if _, e := tx.appendMessage(m); e != nil {
			return e
		}
		for i := 0; i < privateMessageDailyLimit; i++ {
			ev := socialEvent("alice", "bob", "alice", "bob", "message_sent")
			ev.CreatedAt = yesterday
			if e := tx.appendEvent(ev); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "今天可寄", "today"); e != nil {
		t.Fatal("昨天配額未重置", e)
	}
	if e := s.socialTransaction(true, func(tx *socialTx) error {
		for i := 1; i < privateMessageDailyLimit; i++ {
			if e := tx.appendEvent(socialEvent("alice", "bob", "alice", "bob", "message_sent")); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "超額", "over"); e == nil {
		t.Fatal("配額無效")
	}
	if p, e := s.SendPrivateMessage("alice", "bob", "今天可寄", "today"); e != nil || p["duplicate"] != true {
		t.Fatal("配額阻止重試確認", e)
	}
	if p, e := s.ReadPrivateMessages("bob", "alice", "", 10); e != nil || len(p["messages"].([]PrivateMessage)) != 2 {
		t.Fatal("過期資料被刪", e)
	}
}
