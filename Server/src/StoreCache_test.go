package main

import (
	"sync"
	"testing"
	"time"
)

func TestPerBoardLockingConcurrency(t *testing.T) {
	s := NewStore(t.TempDir(), 200, false)
	_, _ = s.CreateRoom("default", "lobby", "Lobby", "Chat", "Main lobby", "root")
	_, _ = s.CreateRoom("default", "sexy", "Sexy", "Adult", "Sexy room", "root")
	_, _ = s.CreateRoom("default", "tech", "Tech", "Tech", "Tech room", "root")

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Parallel writes and reads across 3 different rooms
	for i := 0; i < 30; i++ {
		wg.Add(3)
		go func(idx int) {
			defer wg.Done()
			<-start
			_ = s.AddMessage(Message{
				ProjectID: "default",
				RoomID:    "lobby",
				ID:        "msg-lobby-" + string(rune('a'+idx)),
				Title:     "Lobby Topic",
				Text:      "Hello Lobby",
				AgentID:   "agent-1",
			})
		}(i)

		go func(idx int) {
			defer wg.Done()
			<-start
			_ = s.AddMessage(Message{
				ProjectID: "default",
				RoomID:    "sexy",
				ID:        "msg-sexy-" + string(rune('a'+idx)),
				Title:     "Sexy Topic",
				Text:      "Hello Sexy",
				AgentID:   "agent-2",
			})
		}(i)

		go func(idx int) {
			defer wg.Done()
			<-start
			_ = s.AddMessage(Message{
				ProjectID: "default",
				RoomID:    "tech",
				ID:        "msg-tech-" + string(rune('a'+idx)),
				Title:     "Tech Topic",
				Text:      "Hello Tech",
				AgentID:   "agent-3",
			})
		}(i)
	}

	close(start)
	wg.Wait()

	lobbyArts, err := s.ListArticles("default", "lobby", ArticleRangeOptions{Limit: 100, Simple: true})
	if err != nil || len(lobbyArts) != 30 {
		t.Fatalf("expected 30 lobby articles, got %d (err: %v)", len(lobbyArts), err)
	}

	sexyArts, err := s.ListArticles("default", "sexy", ArticleRangeOptions{Limit: 100, Simple: true})
	if err != nil || len(sexyArts) != 30 {
		t.Fatalf("expected 30 sexy articles, got %d (err: %v)", len(sexyArts), err)
	}

	techArts, err := s.ListArticles("default", "tech", ArticleRangeOptions{Limit: 100, Simple: true})
	if err != nil || len(techArts) != 30 {
		t.Fatalf("expected 30 tech articles, got %d (err: %v)", len(techArts), err)
	}
}

func TestSortedArticlesCacheAndInvalidation(t *testing.T) {
	s := NewStore(t.TempDir(), 200, false)
	_, _ = s.CreateRoom("default", "lobby", "Lobby", "Chat", "Main lobby", "root")

	_ = s.AddMessage(Message{
		ProjectID: "default",
		RoomID:    "lobby",
		ID:        "art-1",
		ArticleID: "art-1",
		Title:     "Topic 1",
		Text:      "Content 1",
		AgentID:   "agent-1",
		TS:        time.Now().Add(-10 * time.Minute),
	})

	_ = s.AddMessage(Message{
		ProjectID: "default",
		RoomID:    "lobby",
		ID:        "art-2",
		ArticleID: "art-2",
		Title:     "Topic 2",
		Text:      "Content 2",
		AgentID:   "agent-2",
		TS:        time.Now().Add(-5 * time.Minute),
	})

	// 1. Initial read builds cache
	arts1, err := s.ListArticles("default", "lobby", ArticleRangeOptions{Limit: 10, Simple: true})
	if err != nil || len(arts1) != 2 {
		t.Fatalf("expected 2 articles, got %d (err: %v)", len(arts1), err)
	}
	if arts1[0].ArticleID != "art-2" {
		t.Fatalf("expected art-2 first, got %s", arts1[0].ArticleID)
	}

	// Verify room signature
	sigBefore, err := s.GetRoomSignature("default", "lobby")
	if err != nil || sigBefore == 0 {
		t.Fatalf("expected non-zero signature, got %d (err: %v)", sigBefore, err)
	}

	// 2. Add new message invalidates cache
	_ = s.AddMessage(Message{
		ProjectID: "default",
		RoomID:    "lobby",
		ID:        "art-3",
		ArticleID: "art-3",
		Title:     "Topic 3",
		Text:      "Content 3",
		AgentID:   "agent-3",
		TS:        time.Now(),
	})

	sigAfter, err := s.GetRoomSignature("default", "lobby")
	if err != nil || sigAfter == sigBefore {
		t.Fatalf("signature should change after new message (before: %d, after: %d)", sigBefore, sigAfter)
	}

	// 3. Second read re-caches and returns newest article
	arts2, err := s.ListArticles("default", "lobby", ArticleRangeOptions{Limit: 10, Simple: true})
	if err != nil || len(arts2) != 3 {
		t.Fatalf("expected 3 articles, got %d (err: %v)", len(arts2), err)
	}
	if arts2[0].ArticleID != "art-3" {
		t.Fatalf("expected art-3 first, got %s", arts2[0].ArticleID)
	}

	// 4. Test optimized GetArticle
	artSummary, err := s.GetArticle("default", "lobby", "art-3")
	if err != nil || artSummary == nil || artSummary.Title != "Topic 3" {
		t.Fatalf("expected art-3 summary, got %v (err: %v)", artSummary, err)
	}
}

func TestLazyLoadingAndSingleFlight(t *testing.T) {
	tempDir := t.TempDir()
	s := NewStore(tempDir, 200, true) // persist to disk
	_, _ = s.CreateRoom("default", "lazy_room", "Lazy Room", "Test", "Test Lazy Room", "root")

	for i := 0; i < 10; i++ {
		_ = s.AddMessage(Message{
			ProjectID: "default",
			RoomID:    "lazy_room",
			ID:        "lazy-msg-" + string(rune('0'+i)),
			Title:     "Lazy Title",
			Text:      "Lazy Text",
			AgentID:   "agent-lazy",
		})
	}

	// Unload memory to simulate lazy state
	stats, err := s.ReleaseRoomMemory("default", "lazy_room")
	if err != nil || !stats.Released {
		t.Fatalf("expected release memory, got %v", stats)
	}

	// Spawn 15 concurrent readers calling EnsureRoomLoaded / ListArticles simultaneously
	var wg sync.WaitGroup
	results := make([][]ArticleSummary, 15)
	start := make(chan struct{})

	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			arts, _ := s.ListArticles("default", "lazy_room", ArticleRangeOptions{Limit: 20, Simple: true})
			results[idx] = arts
		}(i)
	}

	close(start)
	wg.Wait()

	for i, r := range results {
		if len(r) != 10 {
			t.Fatalf("reader %d got %d articles, expected 10", i, len(r))
		}
	}
}

func TestEvictIdleRoomsMemoryGovernance(t *testing.T) {
	s := NewStore(t.TempDir(), 200, false)
	_, _ = s.CreateRoom("default", "room_a", "Room A", "Test", "Test A", "root")
	_, _ = s.CreateRoom("default", "room_b", "Room B", "Test", "Test B", "root")

	_ = s.AddMessage(Message{ProjectID: "default", RoomID: "room_a", ID: "a1", Text: "text A", AgentID: "agent-1"})
	_ = s.AddMessage(Message{ProjectID: "default", RoomID: "room_b", ID: "b1", Text: "text B", AgentID: "agent-2"})

	// Evict with 0 timeout (everything older than now)
	evicted := s.EvictIdleRooms(1 * time.Nanosecond)
	if evicted != 2 {
		t.Fatalf("expected 2 evicted rooms, got %d", evicted)
	}

	// Verify room A is in unloaded state
	s.mu.RLock()
	rA := s.projects["default"].Rooms["room_a"]
	s.mu.RUnlock()

	loaded, msgs, _, _ := rA.Stats()
	if loaded || msgs != 0 {
		t.Fatalf("expected room A unloaded with 0 messages, got loaded=%v msgs=%d", loaded, msgs)
	}
}
