package main

import (
	"context"
	"testing"
	"time"
)

func TestListMessagesAfterCursor(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "lobby", "Lobby", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-3 * time.Second)
	for i, id := range []string{"m1", "m2", "m3"} {
		if err := store.AddMessage(Message{ID: id, ProjectID: "default", RoomID: "lobby", AgentID: "agent-a", ArticleID: id, Text: id, TS: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	facade := &SmallTalkFacade{Store: store}
	got, err := facade.GetNewMessages("agent-a", "default", "lobby", "m1", time.Time{}, 20)
	if err != nil || len(got) != 2 || got[0].ID != "m2" || got[1].ID != "m3" {
		t.Fatalf("after id result=%+v err=%v", got, err)
	}
	got, err = facade.GetNewMessages("agent-a", "default", "lobby", "", base, 20)
	if err != nil || len(got) != 2 || got[0].ID != "m2" {
		t.Fatalf("after ts result=%+v err=%v", got, err)
	}
	if _, err := facade.GetNewMessages("agent-a", "default", "lobby", "m3", time.Time{}, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.GetNewMessages("agent-a", "default", "private", "", time.Time{}, 20); err != ErrRoomNotFound {
		t.Fatalf("missing room err=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err = facade.WaitForNewMessages(ctx, "agent-a", "default", "lobby", "m3", time.Time{}, 20, 20*time.Millisecond)
	if err != nil || len(got) != 0 {
		t.Fatalf("timeout result=%+v err=%v", got, err)
	}
	if err := store.UpsertClientACL("agent-a", nil, []RoomRef{{ProjectID: "default", RoomID: "lobby"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.GetNewMessages("agent-a", "default", "lobby", "", time.Time{}, 20); err != ErrForbidden {
		t.Fatalf("ACL err=%v", err)
	}
}

func TestWaitForNewMessagesEventWakeup(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "lobby", "Lobby", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	facade := &SmallTalkFacade{Store: store}

	start := time.Now()
	done := make(chan []Message, 1)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		msgs, err := facade.WaitForNewMessages(ctx, "agent-a", "default", "lobby", "", time.Time{}, 20, 4*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		done <- msgs
	}()

	// Wait 50ms then publish a message
	time.Sleep(50 * time.Millisecond)
	if err := store.AddMessage(Message{ID: "m-instant", ProjectID: "default", RoomID: "lobby", AgentID: "agent-b", Text: "hello instant"}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("WaitForNewMessages error: %v", err)
	case msgs := <-done:
		elapsed := time.Since(start)
		if len(msgs) == 0 || msgs[0].ID != "m-instant" {
			t.Fatalf("expected m-instant, got %+v", msgs)
		}
		if elapsed >= 3*time.Second {
			t.Fatalf("expected instant wakeup, but took %v", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("WaitForNewMessages did not wake up in time")
	}
}
