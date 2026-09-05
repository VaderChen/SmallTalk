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

// 僅接受測試專用暫存 Unix socket，不讀 DATABASE_URL 或正式設定。
func TestAdminRoleIsolatedPostgresPersistence(t *testing.T) {
	socket := os.Getenv("SMALLTALK_TEST_PG_SOCKET")
	if socket == "" {
		t.Skip("需啟動專用暫存 PostgreSQL")
	}
	if !filepath.IsAbs(socket) || !strings.HasPrefix(filepath.Base(socket), "smalltalk-admin-pg-") {
		t.Fatal("拒絕非隔離 PostgreSQL 路徑")
	}
	dsn := "host=" + socket + " port=25439 user=postgres sslmode=disable"
	bootstrap, err := sql.Open("postgres", dsn+" dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	dbName := fmt.Sprintf("role_test_%d", time.Now().UnixNano())
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
	entry := &AgentRegistryEntry{ClientID: "role-migration", DisplayName: "隔離角色測試", Approved: true, LastSeenAt: time.Now().Format(time.RFC3339Nano)}
	if err := pg.SaveAgentRegistryEntry(entry); err != nil {
		t.Fatal(err)
	}
	// 模擬升級前欄位不存在的結構，驗證可重入的新增欄位遷移。
	if _, err := pg.db.Exec(`ALTER TABLE agent_registry DROP COLUMN is_admin, DROP COLUMN is_admin_at`); err != nil {
		t.Fatal(err)
	}
	if err := pg.initSystemSchema(); err != nil {
		t.Fatal(err)
	}
	if err := pg.initSystemSchema(); err != nil {
		t.Fatal(err)
	}
	store := NewStore(t.TempDir(), 100, false)
	_, err = store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: entry.ClientID, DisplayName: entry.DisplayName, LastSeenAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentAdmin(entry.ClientID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPostgres(pg); err != nil {
		t.Fatal(err)
	}
	loaded, err := pg.LoadAllAgentRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded[entry.ClientID].IsAdmin || loaded[entry.ClientID].IsAdminAt == "" || !loaded[entry.ClientID].postgresRoleLoaded {
		t.Fatal("未保留原有私有 Registry 的管理角色")
	}
	reloaded, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !isAgentAdmin(reloaded, entry.ClientID) {
		t.Fatal("重新載入遺失管理角色")
	}
	if _, err := reloaded.SetAgentAdmin(entry.ClientID, false); err != nil {
		t.Fatal(err)
	}
	// 舊 store 仍留有 true；明確撤權的 PG false 不得被旧資料重新升權。
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	if isAgentAdmin(store, entry.ClientID) {
		t.Fatal("撤銷角色被舊資料覆蓋")
	}
	if _, err := store.SetAgentAdmin(entry.ClientID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	if !isAgentAdmin(store, entry.ClientID) {
		t.Fatal("重新授權未持久化")
	}
	// 管理頁共同保存管理員／版主角色，指派與取消都須跨重新載入保留。
	for _, room := range []string{"role-a", "role-b", "announce", "visitors"} {
		if _, err := store.CreateRoom("default", room, room, "", "", "system"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetAgentRole(entry.ClientID, true, []string{"default/role-a", "default/announce", "default/visitors"}); err != nil {
		t.Fatal(err)
	}
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	admin, rooms, err := store.GetAgentRole(entry.ClientID)
	if err != nil || !admin || len(rooms) != 1 || rooms[0] != "default/role-a" {
		t.Fatal("版主指派未正確保存", err, rooms)
	}
	if store.IsBoardModerator(entry.ClientID, entry.DisplayName, "default", "announce") || store.IsBoardModerator(entry.ClientID, entry.DisplayName, "default", "visitors") {
		t.Fatal("系統看板錯誤開放版主權")
	}
	if err := store.SetAgentRole(entry.ClientID, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	admin, rooms, err = store.GetAgentRole(entry.ClientID)
	if err != nil || admin || len(rooms) != 0 {
		t.Fatal("取消角色未正確保存", err, rooms)
	}
	// 第二筆看板寫入失敗時，第一筆與管理員旗標均須回滾。
	if _, err := pg.db.Exec(`DELETE FROM boards WHERE project_id='default' AND room_id='role-b'`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentRole(entry.ClientID, true, []string{"default/role-a", "default/role-b"}); err == nil {
		t.Fatal("儲存失敗卻回報成功")
	}
	admin, rooms, err = store.GetAgentRole(entry.ClientID)
	if err != nil || admin || len(rooms) != 0 {
		t.Fatal("失敗改變記憶體角色", err, rooms)
	}
	if err := store.LoadFromPostgres(); err != nil {
		t.Fatal(err)
	}
	admin, rooms, err = store.GetAgentRole(entry.ClientID)
	if err != nil || admin || len(rooms) != 0 {
		t.Fatal("交易未回滾", err, rooms)
	}

}
