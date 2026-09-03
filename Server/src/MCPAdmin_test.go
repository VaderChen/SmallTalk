package main

import (
	"context"
	"testing"
	"time"
)

func TestMCPAdminAuthorizationBoundaries(t *testing.T) {
	if _, err := requireMCPRoot(context.Background()); err != ErrForbidden && err == nil {
		t.Fatalf("unauthenticated admin check error=%v", err)
	}
	ctx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{ClientID: "agent-a", PrincipalType: "agent"})
	if _, err := requireMCPRoot(ctx); err != ErrForbidden {
		t.Fatalf("non-root admin check error=%v, want %v", err, ErrForbidden)
	}
	rootCtx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{ClientID: "root", PrincipalType: "root"})
	if _, err := requireMCPRoot(rootCtx); err != nil {
		t.Fatalf("root admin check error=%v", err)
	}
}

func TestMCPAdminStoreOperations(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-a", DisplayName: "Agent A", LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentIssuedToken("agent-a", "admin-token", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentApproval("agent-a", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.GetAgentRegistry("agent-a"); !ok {
		t.Fatal("registered agent was not retrievable")
	}
	if err := store.UpsertClientACL("agent-a", []RoomRef{{ProjectID: "default", RoomID: "public"}}, []RoomRef{{ProjectID: "default", RoomID: "private"}}); err != nil {
		t.Fatal(err)
	}
	acl, ok := store.GetClientACL("agent-a")
	if !ok || len(acl.AllowRooms) != 1 || len(acl.DenyRooms) != 1 {
		t.Fatalf("unexpected ACL: ok=%v acl=%+v", ok, acl)
	}
	if err := store.DeleteAgentRegistry("agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.GetAgentRegistry("agent-a"); ok {
		t.Fatal("deleted agent remained in registry")
	}
}

func TestAgentRoleOperations(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	clientID := "agent-hedgehog"
	displayName := "刺蝟會翻譯"
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: clientID, DisplayName: displayName, LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "emei", "峨嵋派", "武俠", "峨嵋討論板", ""); err != nil {
		t.Fatal(err)
	}

	// 1. Initially not admin, not moderator
	isAdmin, modRooms, err := store.GetAgentRole(clientID)
	if err != nil || isAdmin || len(modRooms) != 0 {
		t.Fatalf("unexpected initial role: isAdmin=%v modRooms=%v err=%v", isAdmin, modRooms, err)
	}
	if store.IsBoardModerator(clientID, displayName, "default", "emei") {
		t.Fatal("should not be board moderator initially")
	}

	// 2. Set as admin and moderator of emei
	if err := store.SetAgentRole(clientID, true, []string{"default/emei"}); err != nil {
		t.Fatal(err)
	}

	isAdmin, modRooms, err = store.GetAgentRole(clientID)
	if err != nil || !isAdmin || len(modRooms) != 1 || modRooms[0] != "default/emei" {
		t.Fatalf("unexpected updated role: isAdmin=%v modRooms=%v err=%v", isAdmin, modRooms, err)
	}
	if !store.IsBoardModerator(clientID, displayName, "default", "emei") {
		t.Fatal("should be board moderator after assignment")
	}

	// 3. Remove moderator, keep admin
	if err := store.SetAgentRole(clientID, true, []string{}); err != nil {
		t.Fatal(err)
	}
	isAdmin, modRooms, err = store.GetAgentRole(clientID)
	if err != nil || !isAdmin || len(modRooms) != 0 {
		t.Fatalf("unexpected role after removing mod: isAdmin=%v modRooms=%v err=%v", isAdmin, modRooms, err)
	}
}
