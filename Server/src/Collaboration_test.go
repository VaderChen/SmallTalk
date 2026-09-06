package main

import (
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func collaborationFixture(t *testing.T, s *Store) {
	t.Helper()
	if e := s.ConfigureCollaboration(filepath.Join(t.TempDir(), "sandbox"), filepath.Join(t.TempDir(), "website")); e != nil {
		t.Fatal(e)
	}
	if e := s.SetCollaborationEnabled(true); e != nil {
		t.Fatal(e)
	}
}
func TestCollaborationMCPWorkflow(t *testing.T) {
	h := chatHTTPFixture(t)
	alice, bob, guest := h.connect("local-social-alice"), h.connect("local-social-bob"), h.connect("")
	create := map[string]any{"name": "協作測試", "request_id": "collab-create", "join_mode": "public"}
	h.call(alice, "smalltalk_create_collaboration", create, true)
	collaborationFixture(t, h.s)
	id := h.call(alice, "smalltalk_create_collaboration", create, false)["room_id"].(string)
	manage := func(action string) map[string]any { return map[string]any{"room_id": id, "action": action} }
	h.call(alice, "smalltalk_manage_collaboration", manage("publish"), true)
	h.call(bob, "smalltalk_manage_collaboration", manage("join"), true)
	args := map[string]any{"room_id": id, "path": "src/api/main.go", "action": "lock"}
	lock := h.call(alice, "smalltalk_collaboration_lock", args, false)
	h.call(alice, "smalltalk_manage_collaboration", manage("publish"), true)
	commit := map[string]any{"room_id": id, "path": "src/api/main.go", "lock_token": lock["lock_token"], "base_revision": 0, "request_id": "seed-one", "content_base64": base64.StdEncoding.EncodeToString([]byte("package main\n"))}
	h.call(alice, "smalltalk_collaboration_commit", commit, false)
	if h.call(alice, "smalltalk_collaboration_commit", commit, false)["duplicate"] != true {
		t.Fatal("重試未去重")
	}
	h.call(bob, "smalltalk_collaboration_read", manage("files"), true)
	api := &BBSAPI{Store: h.s}
	public := func() (int, string) {
		w := httptest.NewRecorder()
		data := api.publicCollaborationHTTP(w, httptest.NewRequest("GET", "/api/collaborations/"+id, nil), []string{"collaborations", id})
		return w.Code, string(data)
	}
	if code, _ := public(); code != 404 {
		t.Fatal("準備區外洩")
	}
	h.call(alice, "smalltalk_manage_collaboration", manage("publish"), false)
	h.call(bob, "smalltalk_manage_collaboration", manage("join"), false)
	h.call(guest, "smalltalk_collaboration_read", manage("files"), true)
	files := h.call(bob, "smalltalk_collaboration_read", manage("files"), false)
	if len(files["files"].([]any)) != 1 {
		t.Fatal(files)
	}
	read := map[string]any{"room_id": id, "path": "src/api/main.go", "action": "read", "revision": 1, "offset": 0, "limit": 7}
	if got := h.call(bob, "smalltalk_collaboration_read", read, false); got["content_base64"] != base64.StdEncoding.EncodeToString([]byte("package")) || got["has_more"] != true {
		t.Fatal(got)
	}
	if code, body := public(); code != 200 || strings.Contains(body, "src/api") || strings.Contains(body, "lock_token") {
		t.Fatalf("公開資料 %d %s", code, body)
	}
	h.call(alice, "smalltalk_manage_collaboration", manage("close"), false)
	h.call(bob, "smalltalk_collaboration_read", read, true)
	h.call(alice, "smalltalk_collaboration_read", read, false)
	if code, _ := public(); code != 404 {
		t.Fatal("關閉後仍公開")
	}
	if e := h.s.SetCollaborationEnabled(false); e != nil {
		t.Fatal(e)
	}
	h.call(alice, "smalltalk_collaboration_read", read, true)
}
func TestCollaborationLockVersions(t *testing.T) {
	s := chatFixture(t, nil)
	collaborationFixture(t, s)
	room, e := s.createChatroomKind("alice", "版本測試", "version-create", "public", true)
	if e != nil {
		t.Fatal(e)
	}
	id := room["room_id"].(string)
	put := func(client, p, request, text string, base int) (map[string]any, error) {
		lock, e := s.CollaborationLock(client, id, p, "lock", "")
		if e != nil {
			return nil, e
		}
		return s.CommitCollaborationFile(client, id, p, lock["lock_token"].(string), request, base64.StdEncoding.EncodeToString([]byte(text)), base)
	}
	if _, e = put("alice", "docs/sub/readme.txt", "v1", "first", 0); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageChatroom("alice", id, "publish", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageChatroom("bob", id, "join", ""); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, c := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			if _, e := s.CollaborationLock(c, id, "docs/sub/readme.txt", "lock", ""); e == nil {
				results <- c
			}
		}(c)
	}
	wg.Wait()
	close(results)
	winners := []string{}
	for c := range results {
		winners = append(winners, c)
	}
	if len(winners) != 1 {
		t.Fatal(winners)
	}
	if _, e = s.CollaborationLock("alice", id, "other/file.txt", "lock", ""); e != nil {
		t.Fatal("不同檔案不應互斥", e)
	}
	if e = s.socialTransaction(true, func(tx *socialTx) error {
		r, e := tx.chatRoom(id)
		if e != nil {
			return e
		}
		f := r.Sandbox.Files["docs/sub/readme.txt"]
		f.Lock.ExpiresAt = time.Now().Add(-time.Second)
		r.Sandbox.Files["docs/sub/readme.txt"] = f
		return tx.saveChat(r)
	}); e != nil {
		t.Fatal(e)
	}
	lock, e := s.CollaborationLock("bob", id, "docs/sub/readme.txt", "lock", "")
	if e != nil {
		t.Fatal(e)
	}
	token := lock["lock_token"].(string)
	if _, e = s.CommitCollaborationFile("bob", id, "docs/sub/readme.txt", token, "stale", base64.StdEncoding.EncodeToString([]byte("second")), 0); e == nil {
		t.Fatal("舊版本被覆蓋")
	}
	if _, e = s.CommitCollaborationFile("bob", id, "docs/sub/readme.txt", token, "v2", base64.StdEncoding.EncodeToString([]byte("second")), 1); e != nil {
		t.Fatal(e)
	}
	physical, e := s.ReadCollaborationFiles("alice", id, "docs/sub/readme.txt", "read", 2, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	blob, e := os.ReadFile(filepath.Join(s.collaborationDir, id, physical["sha256"].(string)))
	if e != nil || string(blob) != "second" {
		t.Fatal("檔案未實際保存到SANDBOX", e)
	}
	old, e := s.ReadCollaborationFiles("alice", id, "docs/sub/readme.txt", "read", 1, 0, 0)
	if e != nil || old["content_base64"] != base64.StdEncoding.EncodeToString([]byte("first")) {
		t.Fatal(old, e)
	}
	for _, p := range []string{"../escape", "/absolute", "x/../../y", "x\\y", "x:y", "a//b", "a/./b"} {
		if _, e = s.CollaborationLock("alice", id, p, "lock", ""); e == nil {
			t.Fatal("允許非法路徑", p)
		}
	}
	if _, e = put("alice", "docs", "collision", "bad", 0); e == nil {
		t.Fatal("檔案覆蓋目錄")
	}
	if _, e = s.ReadCollaborationFiles("charlie", id, "docs/sub/readme.txt", "read", 1, 0, 0); e == nil {
		t.Fatal("非成員讀取")
	}
	if e = s.SetCollaborationEnabled(false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CollaborationLock("alice", id, "new", "lock", ""); e == nil {
		t.Fatal("停用仍可寫")
	}
	if e = s.SetCollaborationEnabled(true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadCollaborationFiles("alice", id, "docs/sub/readme.txt", "history", 0, 0, 0); e != nil {
		t.Fatal("停用破壞歷史", e)
	}
}

func TestCollaborationLeaseRevokedOnLeave(t *testing.T) {
	s := chatFixture(t, nil)
	collaborationFixture(t, s)
	r, e := s.createChatroomKind("alice", "離開鎖定測試", "lease-create", "public", true)
	if e != nil {
		t.Fatal(e)
	}
	id := r["room_id"].(string)
	lock, e := s.CollaborationLock("alice", id, "seed.txt", "lock", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CommitCollaborationFile("alice", id, "seed.txt", lock["lock_token"].(string), "lease-seed", "", 0); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageChatroom("alice", id, "publish", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageChatroom("bob", id, "join", ""); e != nil {
		t.Fatal(e)
	}
	lock, e = s.CollaborationLock("bob", id, "seed.txt", "lock", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageChatroom("bob", id, "leave", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageChatroom("bob", id, "join", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CollaborationLock("bob", id, "seed.txt", "renew", lock["lock_token"].(string)); e == nil {
		t.Fatal("離開後舊鎖復活")
	}
	if _, e = s.CollaborationLock("alice", id, "seed.txt", "lock", ""); e != nil {
		t.Fatal("離開未釋放鎖", e)
	}
	if e = s.SetCollaborationEnabled(false); e != nil {
		t.Fatal(e)
	}
	if e = s.ExportChatroom(id); e != nil {
		t.Fatal("停用不應阻止背景留存匯出", e)
	}
}

func TestCollaborationPostgresPersistence(t *testing.T) {
	pg := isolatedPostgresForTest(t)
	s := chatFixture(t, pg)
	collaborationFixture(t, s)
	r, e := s.createChatroomKind("alice", "PG持久化", "pg-create", "invite", true)
	if e != nil {
		t.Fatal(e)
	}
	id := r["room_id"].(string)
	lock, e := s.CollaborationLock("alice", id, "src/nested/file.txt", "lock", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CommitCollaborationFile("alice", id, "src/nested/file.txt", lock["lock_token"].(string), "pg-seed", "aGVsbG8=", 0); e != nil {
		t.Fatal(e)
	}
	var metadata string
	if e := pg.db.QueryRow(`SELECT payload::text FROM agent_chatrooms WHERE id=$1`, id).Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(metadata, "aGVsbG8=") || strings.Contains(metadata, "content_base64") {
		t.Fatal("DB不應保存檔案內容")
	}
	reload, e := NewStoreWithPostgres(pg, 100)
	if e != nil {
		t.Fatal(e)
	}
	if e = reload.ConfigureCollaboration(s.collaborationDir, filepath.Join(t.TempDir(), "website")); e != nil {
		t.Fatal(e)
	}
	enabled, e := reload.CollaborationEnabled()
	if e != nil || !enabled {
		t.Fatal("功能開關未持久化", e)
	}
	out, e := reload.ReadCollaborationFiles("alice", id, "src/nested/file.txt", "read", 1, 0, 0)
	if e != nil || out["content_base64"] != "aGVsbG8=" {
		t.Fatal("版本未持久化", out, e)
	}
}
