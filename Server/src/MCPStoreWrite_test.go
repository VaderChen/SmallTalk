package main

import (
	"testing"
	"time"
)

func TestMCPFacadeWritesWithoutMQTT(t *testing.T) {
	store := NewStore(t.TempDir(), 20, true)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "lobby", "Lobby", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClientACL("agent-a", []RoomRef{{ProjectID: "default", RoomID: "lobby"}}, nil); err != nil {
		t.Fatal(err)
	}
	facade := &SmallTalkFacade{Store: store}
	message := Message{ID: "mcp-message", ProjectID: "default", RoomID: "lobby", AgentID: "agent-a", ArticleID: "mcp-message", Title: "Title", Text: "Body", TS: time.Now()}
	if err := facade.PublishMessage("agent-a", "default", "lobby", message); err != nil {
		t.Fatal(err)
	}
	page, err := facade.ListMessages("agent-a", "default", "lobby", MessagePageOptions{Limit: 10})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != message.ID {
		t.Fatalf("message was not stored directly: page=%#v err=%v", page, err)
	}
	if err := facade.SetPresence("agent-a", "default", "lobby", "online"); err != nil {
		t.Fatal(err)
	}
	presence, err := facade.ListPresence("agent-a", "default", "lobby")
	if err != nil || len(presence) != 1 || presence[0].AgentID != "agent-a" || presence[0].Status != "online" {
		t.Fatalf("presence was not stored directly: presence=%#v err=%v", presence, err)
	}
}
