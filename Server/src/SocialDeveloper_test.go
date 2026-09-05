package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestSocialRejectIncompleteSnapshot(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"relations":null,"messages":[],"events":[]}`} {
		t.Run(raw, func(t *testing.T) {
			s := socialFixture(t, nil)
			path := filepath.Join(s.dataDir, "social_private.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ManageFriend("alice", "bob", "request"); err == nil {
				t.Error("不完整快照仍允許寫入")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != raw {
				t.Error("既有資料遭覆寫", err)
			}
		})
	}
}

func TestSocialPostgresReloadConcurrencyAudit(t *testing.T) {
	pg := isolatedPostgresForTest(t)
	s := socialFixture(t, pg)
	socialBefriend(t, s, "alice", "bob")
	second, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = second
			}
			if _, err := store.SendPrivateMessage("alice", "bob", "跨 Store 重試", "cross-store"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	var n int
	if err := pg.db.QueryRow(`SELECT COUNT(*) FROM private_messages`).Scan(&n); err != nil || n != 1 {
		t.Fatal("跨連線重複訊息", err, n)
	}
	reload, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reload.ReadPrivateMessages("bob", "alice", "", 10)
	if err != nil || len(page["messages"].([]PrivateMessage)) != 1 {
		t.Fatal("重載遺失訊息", err)
	}
	audit, err := reload.AuditPrivateMessages("auditor", "alice", "bob", "", "測試案件調閱完整紀錄", 1)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pg.db.QueryRow(`SELECT payload FROM social_events WHERE id=$1`, audit["audit_event_id"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var event SocialEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	message := page["messages"].([]PrivateMessage)[0]
	if event.Actor != "auditor" || event.AuditAccount != "alice" || event.AuditPeer != "bob" || event.Reason != "測試案件調閱完整紀錄" || event.Limit != 1 || len(event.MessageIDs) != 1 || event.MessageIDs[0] != message.ID {
		t.Fatal("調閱紀錄不完整", event)
	}
	if event.RetainUntil.Before(event.CreatedAt.AddDate(0, 6, 0)) {
		t.Fatal("調閱留存不足")
	}
}

// 僅在專用測試叢集內將三張社交表備份到另一個全新資料庫。
func TestSocialPostgresBackupRestore(t *testing.T) {
	source := isolatedPostgresForTest(t)
	s := socialFixture(t, source)
	socialBefriend(t, s, "alice", "bob")
	if _, err := s.SendPrivateMessage("alice", "bob", "備份還原後必須保留原文", "backup-message"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuditPrivateMessages("auditor", "alice", "bob", "", "驗證備份還原調閱紀錄", 10); err != nil {
		t.Fatal(err)
	}
	destination := isolatedPostgresForTest(t)
	var sourceName, destName string
	if err := source.db.QueryRow(`SELECT current_database()`).Scan(&sourceName); err != nil {
		t.Fatal(err)
	}
	if err := destination.db.QueryRow(`SELECT current_database()`).Scan(&destName); err != nil {
		t.Fatal(err)
	}
	dir := "/opt/homebrew/opt/postgresql@18/bin/"
	if _, err := os.Stat(dir + "pg_dump"); err != nil {
		t.Skip("本機 PostgreSQL 備份工具不可用")
	}
	archive := filepath.Join(t.TempDir(), "social.dump")
	common := []string{"-h", os.Getenv("SMALLTALK_TEST_PG_SOCKET"), "-p", "25439", "-U", "postgres"}
	args := append(append([]string{}, common...), "-d", sourceName, "--format=custom", "--data-only", "-t", "social_relations", "-t", "private_messages", "-t", "social_events", "-f", archive)
	if raw, err := exec.Command(dir+"pg_dump", args...).CombinedOutput(); err != nil {
		t.Fatalf("備份失敗: %v %s", err, raw)
	}
	args = append(append([]string{}, common...), "-d", destName, "--data-only", "--exit-on-error", archive)
	if raw, err := exec.Command(dir+"pg_restore", args...).CombinedOutput(); err != nil {
		t.Fatalf("還原失敗: %v %s", err, raw)
	}
	restored := socialFixture(t, destination)
	page, err := restored.ReadPrivateMessages("bob", "alice", "", 10)
	if err != nil || len(page["messages"].([]PrivateMessage)) != 1 {
		t.Fatal("還原原文遺失", err)
	}
	if page["messages"].([]PrivateMessage)[0].Text != "備份還原後必須保留原文" {
		t.Fatal("還原原文不同")
	}
	retry, err := restored.SendPrivateMessage("alice", "bob", "備份還原後必須保留原文", "backup-message")
	if err != nil || retry["duplicate"] != true {
		t.Fatal("還原失去去重", err)
	}
	if _, err := restored.SendPrivateMessage("alice", "bob", "還原後新訊息", "after-restore"); err != nil {
		t.Fatal("還原序號失效", err)
	}
	var count int
	if err := destination.db.QueryRow(`SELECT COUNT(*) FROM social_events WHERE kind='admin_read_messages'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("調閱紀錄遺失", err, count)
	}
}
