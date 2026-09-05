package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 只接受明確指定的測試 Unix socket；絕不使用設定檔或自動探測既有資料庫。
// 每個測試各自建立與刪除資料庫，不依賴執行順序或其他測試的資料。
func isolatedPostgresForTest(t *testing.T) *PostgresStore {
	t.Helper()
	socket := os.Getenv("SMALLTALK_TEST_PG_SOCKET")
	if socket == "" {
		t.Skip("PostgreSQL 整合測試需指定 SMALLTALK_TEST_PG_SOCKET 專用暫存叢集")
	}
	if !filepath.IsAbs(socket) || !strings.HasPrefix(filepath.Base(socket), "smalltalk-admin-pg-") {
		t.Fatal("拒絕非隔離的 PostgreSQL socket")
	}
	dsn := func(db string) string {
		u := url.URL{Scheme: "postgres", User: url.User("postgres"), Path: "/" + db}
		q := url.Values{"host": {socket}, "port": {"25439"}, "sslmode": {"disable"}, "connect_timeout": {"3"}}
		u.RawQuery = q.Encode()
		return u.String()
	}
	bootstrap, err := sql.Open("postgres", dsn("postgres"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bootstrap.Close() })
	name := fmt.Sprintf("smalltalk_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := bootstrap.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := bootstrap.Exec("DROP DATABASE " + name); err != nil {
			t.Errorf("清理測試資料庫: %v", err)
		}
	})
	pg, err := NewPostgresStore(dsn(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })
	return pg
}

func seedPostgresArticle(t *testing.T, pg *PostgresStore) Message {
	t.Helper()
	if err := pg.SaveBoardMetadata("default", "sql_fixture", "隔離 SQL 看板", "測試", "自建測資", "system"); err != nil {
		t.Fatal(err)
	}
	msg := Message{ID: "fixture-root", ArticleID: "fixture-root", ProjectID: "default", RoomID: "sql_fixture", AgentID: "fixture-author", DisplayName: "隔離測試作者", Title: "隔離測試主文", Text: "獨立測資", TS: time.Now()}
	if err := pg.InsertMessage(msg); err != nil {
		t.Fatal(err)
	}
	return msg
}
