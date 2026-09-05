package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebViewIsolatedPostgres(t *testing.T) {
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
	name := fmt.Sprintf("webview_test_%d", os.Getpid())
	if _, err := bootstrap.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := bootstrap.Exec("DROP DATABASE " + name); err != nil {
			t.Error(err)
		}
	}()
	pg, err := NewPostgresStore(dsn + " dbname=" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	s, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatal(err)
	}
	testViewLifecycle(t, s)
	// 資料庫錯誤不能回傳成功核准，也不能退回本機檔案。
	token := seedViewAgent(t, s)
	record, browser, err := s.createViewRequest("pg-failure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.db.Exec(`CREATE FUNCTION reject_view_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER reject_view_update BEFORE UPDATE ON browser_view_requests FOR EACH ROW EXECUTE FUNCTION reject_view_update()`); err != nil {
		t.Fatal(err)
	}
	if err := s.approveViewRequest(record.ID, &requestAuthContext{ClientID: "view-agent", TokenKind: "agent", CredentialHash: viewHash(token)}); err == nil {
		t.Fatal("SQL 失敗仍核准")
	}
	got, err := s.pollViewRequest(browser)
	if err != nil || got.Activated || got.ClientID != "" {
		t.Fatal("SQL 失敗留下已核准狀態", err)
	}
	if _, err := pg.db.Exec(`DROP TRIGGER reject_view_update ON browser_view_requests`); err != nil {
		t.Fatal(err)
	}
	if err := s.approveViewRequest(record.ID, &requestAuthContext{ClientID: "view-agent", TokenKind: "agent", CredentialHash: viewHash(token)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pollViewRequest(browser); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.authorizeViewToken(browser); !ok {
		t.Fatal("PostgreSQL 重載遺失唯讀 session")
	}
}
