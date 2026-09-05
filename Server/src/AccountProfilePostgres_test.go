package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileIsolatedPostgres(t *testing.T) {
	socket := os.Getenv("SMALLTALK_TEST_PG_SOCKET")
	if socket == "" {
		t.Skip("需專用暫存 PostgreSQL")
	}
	if !filepath.IsAbs(socket) || !strings.HasPrefix(filepath.Base(socket), "smalltalk-admin-pg-") {
		t.Fatal("拒絕非隔離資料庫")
	}
	dsn := "host=" + socket + " port=25439 user=postgres sslmode=disable"
	bootstrap, err := sql.Open("postgres", dsn+" dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	dbName := fmt.Sprintf("profile_test_%d", time.Now().UnixNano())
	if _, err := bootstrap.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := bootstrap.Exec("DROP DATABASE " + dbName); err != nil {
			t.Error(err)
		}
	}()
	pg, err := NewPostgresStore(dsn + " dbname=" + dbName)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	store, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "profile-writer", DisplayName: "舊名稱"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "profile-other", DisplayName: "另一位"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "name-test", "名稱測試", "", "", "舊名稱"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "profile-writer", DisplayName: "新名稱"}); err != nil {
		t.Fatal(err)
	}
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	entry, _ := store.GetAgentRegistry("profile-writer")
	if entry.RenamedAt == "" || len(entry.NameHistory) != 1 || entry.DisplayName != "新名稱" {
		t.Fatal("名稱歷程未持久化")
	}
	room, _ := store.GetRoom("default", "name-test")
	if room.Owner != "profile-writer" {
		t.Fatal("更名遺失舊版主身份")
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "profile-writer", DisplayName: "再次更名"}); err == nil {
		t.Fatal("重載後繞過冷卻")
	}
	other, _ := store.GetAgentRegistry("profile-other")
	other.DisplayName = "新名稱"
	if err := pg.SaveAgentRegistryEntry(&other); err == nil {
		t.Fatal("SQL 未擋住重名")
	}
	// 在儲存階段注入失敗，驗證名稱／歷程仍維持原值。
	store.mu.Lock()
	store.agentRegistry["profile-writer"].RenamedAt = time.Now().AddDate(0, -2, 0).Format(time.RFC3339Nano)
	store.mu.Unlock()
	if err := store.SaveRegistry(); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.db.Exec(`CREATE FUNCTION reject_profile_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.display_name='rollback' THEN RAISE EXCEPTION 'test failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_profile_test BEFORE UPDATE ON agent_registry FOR EACH ROW EXECUTE FUNCTION reject_profile_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "profile-writer", DisplayName: "rollback"}); err == nil {
		t.Fatal("SQL 失敗卻回報成功")
	}
	entry, _ = store.GetAgentRegistry("profile-writer")
	if entry.DisplayName != "新名稱" || len(entry.NameHistory) != 1 {
		t.Fatal("SQL 失敗未回復記憶體")
	}
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	entry, _ = store.GetAgentRegistry("profile-writer")
	if entry.DisplayName != "新名稱" || len(entry.NameHistory) != 1 {
		t.Fatal("SQL 失敗改變資料")
	}
	// 舊資料的重複名稱應明確拒絕遷移，不能自動替任何人改名。
	if _, err := pg.db.Exec(`UPDATE agent_registry SET display_name='新名稱' WHERE client_id='profile-other'`); err != nil {
		t.Fatal(err)
	}
	if err := pg.initProfileSchema(); err == nil {
		t.Fatal("舊資料重名卻成功建立規則")
	}
}
