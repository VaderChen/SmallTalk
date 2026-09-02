package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoApprovalDiskPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "smalltalk_auto_approval_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewStoreWithError(tmpDir, 100, true)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	if store.AutoApprovalEnabled() {
		t.Fatalf("expected auto-approval to be disabled by default")
	}

	if err := store.SetAutoApprovalConfig(true, 5); err != nil {
		t.Fatalf("SetAutoApprovalConfig(true, 5) failed: %v", err)
	}
	if store.AutoApprovalIntervalMinutes() != 5 {
		t.Fatalf("expected interval 5, got %d", store.AutoApprovalIntervalMinutes())
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(tmpDir, "auto_approval.json")); err != nil {
		t.Fatalf("auto_approval.json not found on disk: %v", err)
	}

	// Reload from disk in a new store
	store2, err := NewStoreWithError(tmpDir, 100, true)
	if err != nil {
		t.Fatalf("reload NewStore failed: %v", err)
	}

	if !store2.AutoApprovalEnabled() {
		t.Fatalf("expected auto-approval to remain enabled after reload")
	}
	if store2.AutoApprovalIntervalMinutes() != 5 {
		t.Fatalf("expected interval 5 after reload, got %d", store2.AutoApprovalIntervalMinutes())
	}

	// Toggle to false with interval 3
	if err := store2.SetAutoApprovalConfig(false, 3); err != nil {
		t.Fatalf("SetAutoApprovalConfig(false, 3) failed: %v", err)
	}

	store3, err := NewStoreWithError(tmpDir, 100, true)
	if err != nil {
		t.Fatalf("reload NewStore failed: %v", err)
	}
	if store3.AutoApprovalEnabled() {
		t.Fatalf("expected auto-approval to be disabled after setting to false")
	}
	if store3.AutoApprovalIntervalMinutes() != 3 {
		t.Fatalf("expected interval 3 after reload, got %d", store3.AutoApprovalIntervalMinutes())
	}
}

func TestAutoApprovalPostgresPersistence(t *testing.T) {
	pg, err := ConnectLocalPostgres()
	if err != nil {
		t.Skipf("Postgres not available, skipping: %v", err)
	}
	defer pg.Close()

	store, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatalf("NewStoreWithPostgres failed: %v", err)
	}

	// Set to true
	if err := store.SetAutoApprovalEnabled(true); err != nil {
		t.Fatalf("SetAutoApprovalEnabled(true) failed: %v", err)
	}

	// Reload with a new store instance connected to Postgres
	store2, err := NewStoreWithPostgres(pg, 100)
	if err != nil {
		t.Fatalf("NewStoreWithPostgres reload failed: %v", err)
	}

	if !store2.AutoApprovalEnabled() {
		t.Fatalf("expected auto-approval to be persisted and enabled in Postgres mode")
	}

	// Test auto-approve pending agents
	agentID := "test-agent-auto-app-" + time.Now().Format("150405")
	_, err = store2.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    agentID,
		DisplayName: "Test Auto Agent",
		MACAddress:  "AA:BB:CC:DD:EE:FF",
		LastSeenAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertAgentRegistry failed: %v", err)
	}

	entry, ok := store2.GetAgentRegistry(agentID)
	if !ok || entry.Approved {
		t.Fatalf("agent should be registered but not approved yet")
	}

	count, err := store2.AutoApprovePendingAgents()
	if err != nil {
		t.Fatalf("AutoApprovePendingAgents failed: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 agent approved, got %d", count)
	}

	entryAfter, ok := store2.GetAgentRegistry(agentID)
	if !ok || !entryAfter.Approved {
		t.Fatalf("agent should be approved now")
	}
	if entryAfter.Token == "" {
		t.Fatalf("agent should have received a token")
	}

	// Clean up
	_ = store2.DeleteAgentRegistry(agentID)
}
