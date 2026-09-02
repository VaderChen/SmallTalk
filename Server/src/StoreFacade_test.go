package main

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T, persist bool) *Store {
	t.Helper()
	s := NewStore(t.TempDir(), 200, persist)
	if _, err := s.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRoom("default", "lobby", "Lobby", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStorePersistenceReload(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 20, true)
	if _, err := s.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRoom("default", "lobby", "Lobby", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ID: "m1", ProjectID: "default", RoomID: "lobby", AgentID: "agent-a", ArticleID: "a1", Title: "Title", Text: "Body", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	reloaded := NewStore(dir, 20, true)
	items, err := reloaded.ListMessages("default", "lobby", 20)
	if err != nil || len(items) != 1 || items[0].ID != "m1" {
		t.Fatalf("reload items=%+v err=%v", items, err)
	}
}

func TestArticleEditRules(t *testing.T) {
	s := testStore(t, false)
	if err := s.AddMessage(Message{ID: "root", ProjectID: "default", RoomID: "lobby", AgentID: "agent-a", ArticleID: "a1", Title: "Old", Text: "Old body", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ID: "reply", ProjectID: "default", RoomID: "lobby", AgentID: "agent-b", ArticleID: "a1", ReplyToMessageID: "root", Text: "Reply", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateArticleRoot("default", "lobby", "root", "New", "New body", "agent-b"); err != ErrForbidden {
		t.Fatalf("wrong author err=%v", err)
	}
	if _, err := s.UpdateArticleRoot("default", "lobby", "reply", "New", "New body", "agent-a"); err == nil {
		t.Fatal("reply was editable")
	}
	updated, err := s.UpdateArticleRoot("default", "lobby", "root", "New", "New body", "agent-a")
	if err != nil || updated.Title != "New" {
		t.Fatalf("edit result=%+v err=%v", updated, err)
	}
}

func TestStoreConcurrentReadWrite(t *testing.T) {
	s := testStore(t, false)
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.AddMessage(Message{ID: fmt.Sprintf("m-%d", i), ProjectID: "default", RoomID: "lobby", AgentID: "agent-a", Text: "body", TS: time.Now()})
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.ListMessages("default", "lobby", n) }()
	}
	wg.Wait()
	items, err := s.ListMessages("default", "lobby", n)
	if err != nil || len(items) != n {
		t.Fatalf("concurrent items=%d err=%v", len(items), err)
	}
}

func TestFacadeMissingAndForbiddenResources(t *testing.T) {
	s := testStore(t, false)
	f := &SmallTalkFacade{Store: s}
	if _, err := f.GetArticle("agent-a", "default", "missing", "a1"); err != ErrRoomNotFound {
		t.Fatalf("missing room err=%v", err)
	}
	if _, err := f.CreateRoom("agent-a", "default", "new", "New", "", "", ""); err != ErrForbidden {
		t.Fatalf("non-root create err=%v", err)
	}
	if _, err := f.CreateRoom("root", "default", "new", "New", "", "", "root"); err != nil {
		t.Fatal(err)
	}
}

func TestBBSAPIFacadeIntegration(t *testing.T) {
	s := testStore(t, false)
	f := &SmallTalkFacade{Store: s}
	api := &BBSAPI{Facade: f, Store: s}

	if err := s.AddMessage(Message{ID: "m1", ProjectID: "default", RoomID: "lobby", AgentID: "root", ArticleID: "m1", Title: "Notice", Text: "Hello BBS", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://localhost/api/boards/lobby/messages?limit=10", nil)
	respBytes := api.Process(nil, req, nil, nil, nil, "")
	if len(respBytes) == 0 {
		t.Fatalf("empty response from BBSAPI")
	}

	searchReq, _ := http.NewRequest(http.MethodGet, "http://localhost/api/search/messages?q=Notice", nil)
	searchResp := api.Process(nil, searchReq, nil, nil, nil, "")
	if len(searchResp) == 0 {
		t.Fatalf("empty response from BBSAPI search")
	}
}

