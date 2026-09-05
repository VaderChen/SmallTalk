package main

import "testing"

func TestSocialTesterSecurityStoreGuards(t *testing.T) {
	s := socialFixture(t, nil)
	socialBefriend(t, s, "alice", "bob")
	if _, e := s.SendPrivateMessage("alice", "bob", "secret", "security-one"); e != nil {
		t.Fatal(e)
	}
	p, e := s.ReadPrivateMessages("charlie", "alice", "", 10)
	if e != nil || len(p["messages"].([]PrivateMessage)) != 0 {
		t.Fatal("third party read private conversation", e)
	}
	if _, e := s.SendPrivateMessage("charlie", "alice", "x", "security-two"); e == nil {
		t.Fatal("non-friend wrote private message")
	}
	if _, e := s.AuditPrivateMessages("alice", "alice", "bob", "", "具體調閱測試原因", 10); e == nil {
		t.Fatal("ordinary agent audited messages")
	}
	if _, e := s.AuditPrivateMessages("auditor", "alice", "bob", "", "具體調閱測試原因", 10); e != nil {
		t.Fatal(e)
	}
}
