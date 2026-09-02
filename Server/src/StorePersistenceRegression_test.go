package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectRoomStorageIsolationAndReload(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, 20, true)
	for _, projectID := range []string{"project-a", "project-b"} {
		if _, err := store.CreateProject(projectID, projectID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateRoom(projectID, "general", projectID, "", "", "root"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AddMessage(Message{ID: "a1", ProjectID: "project-a", RoomID: "general", AgentID: "agent-a", Text: "A", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(Message{ID: "b1", ProjectID: "project-b", RoomID: "general", AgentID: "agent-b", Text: "B", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	pathA := store.roomMessagesPath("project-a", "general")
	pathB := store.roomMessagesPath("project-b", "general")
	if pathA == pathB {
		t.Fatalf("project room paths collide: %q", pathA)
	}
	if _, err := os.Stat(pathA); err != nil {
		t.Fatalf("project-a message path: %v", err)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Fatalf("project-b message path: %v", err)
	}
	if filepath.Dir(pathA) == filepath.Dir(pathB) {
		t.Fatalf("project room directories collide: %q", filepath.Dir(pathA))
	}

	reloaded := NewStore(dir, 20, true)
	messagesA, err := reloaded.ListMessages("project-a", "general", 20)
	if err != nil || len(messagesA) != 1 || messagesA[0].ID != "a1" {
		t.Fatalf("project-a reload=%+v err=%v", messagesA, err)
	}
	messagesB, err := reloaded.ListMessages("project-b", "general", 20)
	if err != nil || len(messagesB) != 1 || messagesB[0].ID != "b1" {
		t.Fatalf("project-b reload=%+v err=%v", messagesB, err)
	}
}

func TestPresencePersistenceReload(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, 20, true)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "lobby", "Lobby", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.SetPresence("default", "lobby", "agent-a", "online", seen); err != nil {
		t.Fatal(err)
	}

	reloaded := NewStore(dir, 20, true)
	presence, err := reloaded.ListPresence("default", "lobby")
	if err != nil || len(presence) != 1 || presence[0].AgentID != "agent-a" || presence[0].Status != "online" || !presence[0].LastSeen.Equal(seen) {
		t.Fatalf("presence reload=%+v err=%v", presence, err)
	}
}

func TestMigrateLegacyBoards(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "boards", "legacy-room")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"project_id":"project-a","id":"legacy-room","name":"Legacy"}`)
	if err := os.WriteFile(filepath.Join(legacy, "board.json"), meta, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "messages.jsonl"), []byte(`{"id":"m1","project_id":"project-a","room_id":"legacy-room"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyBoards(dir); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "boards", safeStorageComponent("project-a"), safeStorageComponent("legacy-room"))
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still exists, err=%v", err)
	}
}

func TestMigrateLegacyBoardsRefusesUnknownProjectAndCollision(t *testing.T) {
	unknownDir := t.TempDir()
	unknown := filepath.Join(unknownDir, "boards", "room")
	if err := os.MkdirAll(unknown, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "messages.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyBoards(unknownDir); err == nil {
		t.Fatal("unknown project was accepted")
	}

	collisionDir := t.TempDir()
	legacy := filepath.Join(collisionDir, "boards", "room")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "board.json"), []byte(`{"project_id":"p","id":"room"}`), 0644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(collisionDir, "boards", safeStorageComponent("p"), safeStorageComponent("room"))
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyBoards(collisionDir); err == nil {
		t.Fatal("destination collision was accepted")
	}
}
