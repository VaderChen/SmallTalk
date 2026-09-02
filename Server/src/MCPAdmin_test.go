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
