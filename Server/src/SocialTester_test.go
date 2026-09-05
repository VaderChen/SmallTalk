package main

import "testing"

// Tester-owned boundary coverage.  Uses the local temporary store only.
func TestSocialTesterRelationBoundaries(t *testing.T) {
	s := socialFixture(t, nil)
	if _, e := s.ManageFriend("alice", "bob", "request"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageFriend("bob", "alice", "reject"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "bob", "x", "reject"); e == nil {
		t.Fatal("rejected pair could message")
	}
	if _, e := s.ManageFriend("alice", "charlie", "request"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageFriend("alice", "charlie", "cancel"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "charlie", "x", "cancel"); e == nil {
		t.Fatal("cancelled pair could message")
	}
	socialBefriend(t, s, "alice", "auditor")
	if _, e := s.ManageFriend("alice", "auditor", "remove"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("alice", "auditor", "x", "removed"); e == nil {
		t.Fatal("removed pair could message")
	}
	socialBefriend(t, s, "bob", "charlie")
	if _, e := s.ManageFriend("bob", "charlie", "block"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageFriend("charlie", "bob", "block"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ManageFriend("bob", "charlie", "unblock"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("bob", "charlie", "x", "blocked"); e == nil {
		t.Fatal("one-sided remaining block could message")
	}
	if _, e := s.ManageFriend("charlie", "bob", "unblock"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendPrivateMessage("bob", "charlie", "x", "after-unblock"); e == nil {
		t.Fatal("unblock restored friendship")
	}
}
