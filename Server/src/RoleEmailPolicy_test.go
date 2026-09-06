package main

import (
	"testing"
	"time"
)

func seedRoleEmail(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, ok := s.GetAgentRegistry(id); !ok {
		if _, e := s.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: id, DisplayName: id}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.SetAgentApproval(id, true, time.Now()); e != nil {
		t.Fatal(e)
	}
	m := s.roleEmailManager.Load()
	if m == nil {
		m = &EmailManager{store: s, state: emailVerificationState{Bindings: map[string]EmailBinding{}}}
		s.roleEmailManager.Store(m)
	}
	m.mu.Lock()
	m.state.Bindings[id] = EmailBinding{ClientID: id, EmailHash: "test-only-hash", VerifiedAt: time.Now().Format(time.RFC3339Nano)}
	m.mu.Unlock()
}
func TestRoleRequiresVerifiedEmail(t *testing.T) {
	s := socialFixture(t, nil)
	if _, e := s.SetAgentAdmin("alice", true); e == nil {
		t.Fatal("未綁定Email可成為管理員")
	}
	if e := s.SetAgentRole("alice", false, []string{"board"}); e == nil {
		t.Fatal("未綁定Email可成為版主")
	}
	seedRoleEmail(t, s, "alice")
	m := s.roleEmailManager.Load()
	m.mu.Lock()
	b := m.state.Bindings["alice"]
	b.VerifiedAt = ""
	m.state.Bindings["alice"] = b
	m.mu.Unlock()
	if _, e := s.SetAgentAdmin("alice", true); e == nil {
		t.Fatal("未確認Email可成為管理員")
	}
	seedRoleEmail(t, s, "alice")
	if _, e := s.SetAgentAdmin("alice", true); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetAgentAdmin("alice", false); e != nil {
		t.Fatal(e)
	}
}
