package main

import (
	"os"
	"testing"
	"time"
)

func TestRoleSettingsLocalPersistenceAndFailure(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, 20, false)
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "role-owner", DisplayName: "舊版主名稱", LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "role-local", "版主測試", "", "", "舊版主名稱"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentRole("role-owner", true, []string{"default/role-local"}); err != nil {
		t.Fatal(err)
	}
	r, _ := store.GetRoom("default", "role-local")
	if r.Owner != "role-owner" {
		t.Fatal("舊名稱指派未正規化")
	}
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "role-owner", DisplayName: "新版主名稱", LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	loaded := NewStore(dir, 20, false)
	if !loaded.IsBoardModerator("role-owner", "新版主名稱", "default", "role-local") || !isAgentAdmin(loaded, "role-owner") {
		t.Fatal("更名或重新載入遺失角色")
	}
	// Registry 寫入失敗時，不可留下已取消的版主或管理員狀態。
	path := loaded.registryPath()
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := loaded.SetAgentRole("role-owner", false, nil); err == nil {
		t.Fatal("保存失敗卻回報成功")
	}
	if !loaded.IsBoardModerator("role-owner", "新版主名稱", "default", "role-local") || !isAgentAdmin(loaded, "role-owner") {
		t.Fatal("失敗未回復記憶體角色")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".original", path); err != nil {
		t.Fatal(err)
	}
	restored := NewStore(dir, 20, false)
	if !restored.IsBoardModerator("role-owner", "新版主名稱", "default", "role-local") {
		t.Fatal("失敗未回復看板檔案")
	}
	if err := restored.SetAgentRole("role-owner", false, nil); err != nil {
		t.Fatal(err)
	}
	restored = NewStore(dir, 20, false)
	if restored.IsBoardModerator("role-owner", "新版主名稱", "default", "role-local") || isAgentAdmin(restored, "role-owner") {
		t.Fatal("角色取消未保存")
	}
}
