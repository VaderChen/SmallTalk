package main

import (
	"context"
	"testing"
	"time"
)

func TestAgentReadOnlyManualAndAutomaticInactivity(t *testing.T) {
	store := NewStore(t.TempDir(), 100, false)
	clientID := "agent-test-ro"

	// Register agent
	_, err := store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    clientID,
		DisplayName: "Test ReadOnly Agent",
		MACAddress:  "00:11:22:33:44:55",
		LastSeenAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertAgentRegistry failed: %v", err)
	}

	// 1. Initial state is not read-only
	if store.IsAgentReadOnly(clientID) {
		t.Fatalf("agent should not be read-only initially")
	}

	// 2. Set manually to read-only
	entry, err := store.SetAgentReadOnly(clientID, true, time.Now())
	if err != nil {
		t.Fatalf("SetAgentReadOnly(true) failed: %v", err)
	}
	if !entry.ReadOnly || !store.IsAgentReadOnly(clientID) {
		t.Fatalf("agent should be read-only now")
	}

	// 3. Unset read-only
	entry, err = store.SetAgentReadOnly(clientID, false, time.Now())
	if err != nil {
		t.Fatalf("SetAgentReadOnly(false) failed: %v", err)
	}
	if entry.ReadOnly || store.IsAgentReadOnly(clientID) {
		t.Fatalf("agent should not be read-only after unsetting")
	}

	// 4. Test 30-day inactivity automatic read-only
	oldTime := time.Now().Add(-35 * 24 * time.Hour)
	_, _ = store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:   clientID,
		LastSeenAt: oldTime,
	})

	if !store.IsAgentReadOnly(clientID) {
		t.Fatalf("agent with 35 days of inactivity should be detected as read-only")
	}

	list := store.ListAgentRegistry()
	found := false
	for _, a := range list {
		if a.ClientID == clientID {
			found = true
			if !a.ReadOnly {
				t.Fatalf("agent in list should have ReadOnly=true due to inactivity")
			}
		}
	}
	if !found {
		t.Fatalf("agent not found in list")
	}
}

func TestMCPReadOnlyAgentCannotWrite(t *testing.T) {
	store := NewStore(t.TempDir(), 100, false)
	clientID := "agent-mcp-ro"
	token := "token-ro"
	now := time.Now()

	_, _ = store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    clientID,
		DisplayName: "MCP RO Agent",
		MACAddress:  "00:11:22:33:44:55",
		LastSeenAt:  now,
	})
	_, _ = store.SetAgentApproval(clientID, true, now)
	_, _ = store.SetAgentIssuedToken(clientID, token, now, now.Add(time.Hour))
	_, _ = store.UpsertAuthToken(AuthTokenRecord{
		Token:    token,
		ClientID: clientID,
		Kind:     "dev-short",
	}, true)

	facade := &SmallTalkFacade{Store: store}
	server := NewMCPServer(facade)

	// In read-only mode
	_, _ = store.SetAgentReadOnly(clientID, true, now)

	ctx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{
		ClientID:      clientID,
		PrincipalType: "agent",
		TokenKind:     "dev-short",
	})

	// Try write tool
	_, err := requireMCPWrite(ctx, facade)
	if err == nil {
		t.Fatalf("expected requireMCPWrite to fail for read-only agent")
	}

	// Unset read-only mode
	_, _ = store.SetAgentReadOnly(clientID, false, now)
	_, err = requireMCPWrite(ctx, facade)
	if err != nil {
		t.Fatalf("requireMCPWrite should succeed after lifting read-only mode: %v", err)
	}
	_ = server
}
