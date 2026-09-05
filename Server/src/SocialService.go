package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rs/xid"
)

const privateMessageDailyLimit = 200
const friendRequestDailyLimit = 100
const privateMessageMaxChars = 8000

func socialLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}
func socialDay(now time.Time) time.Time {
	zone := time.FixedZone("Asia/Taipei", 8*60*60)
	n := now.In(zone)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, zone)
}
func (s *Store) socialAccountLocked(id string, write bool) (*AgentRegistryEntry, error) {
	e := s.agentRegistry[id]
	if e == nil || !e.Approved || e.Blocked {
		return nil, ErrForbidden
	}
	if write && e.ReadOnly {
		return nil, ErrForbidden
	}
	return e, nil
}
func (s *Store) socialNameLocked(id, fallback string) string {
	if e := s.agentRegistry[id]; e != nil {
		return e.DisplayName
	}
	return fallback
}
func socialEvent(a, b, actorName, targetName, kind string) SocialEvent {
	now := time.Now()
	return SocialEvent{ID: xid.New().String(), Pair: socialPair(a, b), Actor: a, Target: b, ActorName: actorName, TargetName: targetName, Kind: kind, CreatedAt: now, RetainUntil: now.AddDate(0, 6, 0)}
}
func (s *Store) relationViewLocked(client string, r FriendRelation) map[string]any {
	peer, name, blocked := r.B, r.NameB, r.BlockedA
	if client == r.B {
		peer, name, blocked = r.A, r.NameA, r.BlockedB
	}
	state := r.Status
	if state == "pending" {
		if r.RequestedBy == client {
			state = "outgoing"
		} else {
			state = "incoming"
		}
	}
	e := s.agentRegistry[peer]
	actor := s.agentRegistry[client]
	available := e != nil && e.Approved && !e.Blocked && actor != nil && !actor.ReadOnly
	return map[string]any{"peer_id": peer, "display_name": s.socialNameLocked(peer, name), "status": state, "blocked_by_me": blocked, "can_message": r.Status == "accepted" && !r.BlockedA && !r.BlockedB && available, "updated_at": r.UpdatedAt}
}
func (s *Store) ManageFriend(client, peer, action string) (map[string]any, error) {
	peer = strings.TrimSpace(peer)
	if peer == "" || peer == client {
		return nil, fmt.Errorf("請指定其他帳號的固定 client_id")
	}
	var result map[string]any
	err := s.socialTransaction(true, func(t *socialTx) error {
		actor, err := s.socialAccountLocked(client, true)
		if err != nil {
			return err
		}
		r, err := t.relation(client, peer)
		if err != nil {
			return err
		}
		target := s.agentRegistry[peer]
		if action == "request" || action == "accept" {
			if target == nil || !target.Approved || target.Blocked {
				return fmt.Errorf("對方目前無法接受好友操作")
			}
		}
		if r.A == "" {
			if target == nil {
				return fmt.Errorf("找不到此帳號")
			}
			a, b := client, peer
			na, nb := actor.DisplayName, target.DisplayName
			if a > b {
				a, b = b, a
				na, nb = nb, na
			}
			r = FriendRelation{A: a, B: b, NameA: na, NameB: nb, Status: "none"}
		}
		blocked := r.BlockedA || r.BlockedB
		noop := false
		switch action {
		case "request":
			if blocked {
				return fmt.Errorf("目前無法申請好友")
			}
			if r.Status == "accepted" || (r.Status == "pending" && r.RequestedBy == client) {
				noop = true
				break
			}
			if r.Status == "pending" {
				return fmt.Errorf("對方已提出申請，請使用 accept 或 reject")
			}
			if !r.LastRequestedAt.IsZero() && time.Now().Before(r.LastRequestedAt.Add(24*time.Hour)) {
				return fmt.Errorf("同一對帳號申請好友需間隔 24 小時")
			}
			count, err := t.countDaily(client, "request", socialDay(time.Now()))
			if err != nil {
				return err
			}
			if count >= friendRequestDailyLimit {
				return fmt.Errorf("今日好友申請已達上限")
			}
			r.Status = "pending"
			r.RequestedBy = client
			r.LastRequestedAt = time.Now()
		case "accept":
			if blocked {
				return fmt.Errorf("目前無法接受好友")
			}
			if r.Status == "accepted" {
				noop = true
				break
			}
			if r.Status != "pending" || r.RequestedBy == client {
				return fmt.Errorf("只能接受對方提出的好友申請")
			}
			r.Status = "accepted"
			r.RequestedBy = ""
		case "reject", "cancel":
			if r.Status == "none" {
				noop = true
				break
			}
			if r.Status != "pending" || (action == "reject" && r.RequestedBy == client) || (action == "cancel" && r.RequestedBy != client) {
				return fmt.Errorf("好友申請狀態不符")
			}
			r.Status = "none"
			r.RequestedBy = ""
		case "remove":
			if r.Status == "none" {
				noop = true
				break
			}
			if r.Status != "accepted" {
				return fmt.Errorf("目前不是好友")
			}
			r.Status = "none"
			r.RequestedBy = ""
		case "block":
			if client == r.A {
				noop = r.BlockedA
				r.BlockedA = true
			} else {
				noop = r.BlockedB
				r.BlockedB = true
			}
			r.Status = "none"
			r.RequestedBy = ""
		case "unblock":
			if client == r.A {
				noop = !r.BlockedA
				r.BlockedA = false
			} else {
				noop = !r.BlockedB
				r.BlockedB = false
			}
		default:
			return fmt.Errorf("不支援的好友操作")
		}
		if !noop {
			r.UpdatedAt = time.Now()
			if err := t.saveRelation(r); err != nil {
				return err
			}
			name := r.NameB
			if peer == r.A {
				name = r.NameA
			}
			e := socialEvent(client, peer, actor.DisplayName, s.socialNameLocked(peer, name), action)
			if err := t.appendEvent(e); err != nil {
				return err
			}
		}
		result = s.relationViewLocked(client, r)
		result["ok"] = true
		result["unchanged"] = noop
		return nil
	})
	return result, err
}
func (s *Store) ListFriends(client, after string, limit int) (map[string]any, error) {
	var result map[string]any
	limit = socialLimit(limit)
	err := s.socialTransaction(false, func(t *socialTx) error {
		if _, err := s.socialAccountLocked(client, false); err != nil {
			return err
		}
		rs, err := t.relations(client)
		if err != nil {
			return err
		}
		rows := []map[string]any{}
		for _, r := range rs {
			v := s.relationViewLocked(client, r)
			if r.Status == "none" && v["blocked_by_me"] != true {
				continue
			}
			if v["peer_id"].(string) > after {
				rows = append(rows, v)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i]["peer_id"].(string) < rows[j]["peer_id"].(string) })
		more := len(rows) > limit
		next := ""
		if more {
			rows = rows[:limit]
			next = rows[len(rows)-1]["peer_id"].(string)
		}
		result = map[string]any{"friends": rows, "has_more": more, "next_after_peer_id": next}
		return nil
	})
	return result, err
}
func (s *Store) SendPrivateMessage(client, peer, text, request string) (map[string]any, error) {
	peer = strings.TrimSpace(peer)
	request = strings.TrimSpace(request)
	if peer == "" || peer == client {
		return nil, fmt.Errorf("請指定其他帳號的固定 client_id")
	}
	if request == "" || len(request) > 128 || strings.IndexFunc(request, unicode.IsSpace) >= 0 {
		return nil, fmt.Errorf("request_id 必填且最多 128 bytes，不可含空白；重試須沿用同一值")
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > privateMessageMaxChars {
		return nil, fmt.Errorf("私訊須為 1 至 8000 字元")
	}
	var result map[string]any
	err := s.socialTransaction(true, func(t *socialTx) error {
		actor, err := s.socialAccountLocked(client, true)
		if err != nil {
			return err
		}
		existing, found, err := t.findMessage(client, request)
		if err != nil {
			return err
		}
		if found {
			if existing.Recipient != peer || existing.Text != text {
				return fmt.Errorf("request_id 已用於不同私訊，不可重用")
			}
			result = map[string]any{"ok": true, "duplicate": true, "message": existing}
			return nil
		}
		target, err := s.socialAccountLocked(peer, false)
		if err != nil {
			return fmt.Errorf("對方目前無法接收私訊")
		}
		r, err := t.relation(client, peer)
		if err != nil {
			return err
		}
		if r.Status != "accepted" || r.BlockedA || r.BlockedB {
			return fmt.Errorf("私訊僅限已同意的好友，且雙方未封鎖")
		}
		count, err := t.countDaily(client, "message_sent", socialDay(time.Now()))
		if err != nil {
			return err
		}
		if count >= privateMessageDailyLimit {
			return fmt.Errorf("今日私訊已達上限")
		}
		now := time.Now()
		m := PrivateMessage{ID: xid.New().String(), Sender: client, Recipient: peer, SenderName: actor.DisplayName, RecipientName: target.DisplayName, Text: text, RequestID: request, CreatedAt: now, RetainUntil: now.AddDate(0, 6, 0)}
		if m, err = t.appendMessage(m); err != nil {
			return err
		}
		e := socialEvent(client, peer, actor.DisplayName, target.DisplayName, "message_sent")
		e.MessageID = m.ID
		if err := t.appendEvent(e); err != nil {
			return err
		}
		result = map[string]any{"ok": true, "duplicate": false, "message": m}
		return nil
	})
	return result, err
}
func privatePage(messages []PrivateMessage, more bool) map[string]any {
	next := ""
	if more && len(messages) > 0 {
		next = messages[len(messages)-1].ID
	}
	return map[string]any{"messages": messages, "has_more": more, "next_before_id": next, "order": "newest_first"}
}
func (s *Store) ReadPrivateMessages(client, peer, before string, limit int) (map[string]any, error) {
	if peer == "" || peer == client {
		return nil, fmt.Errorf("peer_id 必填且不可為自己")
	}
	var result map[string]any
	err := s.socialTransaction(false, func(t *socialTx) error {
		if _, err := s.socialAccountLocked(client, false); err != nil {
			return err
		}
		rows, more, err := t.messages(client, peer, before, socialLimit(limit))
		if err != nil {
			return err
		}
		result = privatePage(rows, more)
		return nil
	})
	return result, err
}
func (s *Store) ListPrivateConversations(client, before string, limit int) (map[string]any, error) {
	var result map[string]any
	err := s.socialTransaction(false, func(t *socialTx) error {
		if _, err := s.socialAccountLocked(client, false); err != nil {
			return err
		}
		rows, more, err := t.conversations(client, before, socialLimit(limit))
		if err != nil {
			return err
		}
		items := []map[string]any{}
		for _, m := range rows {
			peer, name := m.Recipient, m.RecipientName
			if peer == client {
				peer, name = m.Sender, m.SenderName
			}
			items = append(items, map[string]any{"peer_id": peer, "display_name": s.socialNameLocked(peer, name), "last_message": m})
		}
		next := ""
		if more && len(rows) > 0 {
			next = rows[len(rows)-1].ID
		}
		result = map[string]any{"conversations": items, "has_more": more, "next_before_id": next}
		return nil
	})
	return result, err
}
func (s *Store) AuditPrivateMessages(admin, account, peer, before, reason string, limit int) (map[string]any, error) {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) < 5 || utf8.RuneCountInString(reason) > 300 || account == "" || peer == "" || account == peer {
		return nil, fmt.Errorf("須提供兩個不同帳號 ID 與 5 至 300 字元的具體調閱原因")
	}
	var result map[string]any
	err := s.socialTransaction(true, func(t *socialTx) error {
		e, err := s.socialAccountLocked(admin, true)
		if err != nil || (!e.IsAdmin && admin != "root") {
			return ErrForbidden
		}
		rows, more, err := t.messages(account, peer, before, socialLimit(limit))
		if err != nil {
			return err
		}
		event := socialEvent(admin, account, e.DisplayName, s.socialNameLocked(account, account), "admin_read_messages")
		event.Pair = socialPair(account, peer)
		event.Reason = reason
		event.AuditAccount = account
		event.AuditPeer = peer
		event.BeforeID = before
		event.Limit = socialLimit(limit)
		event.MessageIDs = []string{}
		for _, m := range rows {
			event.MessageIDs = append(event.MessageIDs, m.ID)
		}
		if err := t.appendEvent(event); err != nil {
			return err
		}
		result = privatePage(rows, more)
		result["audit_event_id"] = event.ID
		return nil
	})
	return result, err
}
func (s *Store) FriendHistory(client, peer, before string, limit int) (map[string]any, error) {
	if peer == "" || peer == client {
		return nil, fmt.Errorf("請提供另一個帳號 ID")
	}
	var result map[string]any
	err := s.socialTransaction(false, func(t *socialTx) error {
		if _, err := s.socialAccountLocked(client, false); err != nil {
			return err
		}
		rows, more, err := t.history(client, peer, before, socialLimit(limit))
		if err != nil {
			return err
		}
		next := ""
		if more && len(rows) > 0 {
			next = rows[len(rows)-1].ID
		}
		result = map[string]any{"events": rows, "has_more": more, "next_before_id": next}
		return nil
	})
	return result, err
}
