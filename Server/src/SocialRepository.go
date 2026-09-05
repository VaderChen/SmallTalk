package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type FriendRelation struct {
	A               string    `json:"member_a"`
	B               string    `json:"member_b"`
	NameA           string    `json:"name_a"`
	NameB           string    `json:"name_b"`
	Status          string    `json:"status"`
	RequestedBy     string    `json:"requested_by,omitempty"`
	LastRequestedAt time.Time `json:"last_requested_at"`
	BlockedA        bool      `json:"blocked_a"`
	BlockedB        bool      `json:"blocked_b"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type PrivateMessage struct {
	ID            string    `json:"id"`
	Sender        string    `json:"sender_id"`
	Recipient     string    `json:"recipient_id"`
	SenderName    string    `json:"sender_name_at_send"`
	RecipientName string    `json:"recipient_name_at_send"`
	Text          string    `json:"text"`
	RequestID     string    `json:"request_id"`
	CreatedAt     time.Time `json:"created_at"`
	RetainUntil   time.Time `json:"retain_until"`
	Seq           int64     `json:"-"`
}
type SocialEvent struct {
	AuditAccount string    `json:"audit_account_id,omitempty"`
	AuditPeer    string    `json:"audit_peer_id,omitempty"`
	BeforeID     string    `json:"before_id,omitempty"`
	Limit        int       `json:"limit,omitempty"`
	MessageIDs   []string  `json:"message_ids,omitempty"`
	ID           string    `json:"id"`
	Pair         string    `json:"-"`
	Actor        string    `json:"actor_id"`
	Target       string    `json:"target_id"`
	ActorName    string    `json:"actor_name_at_event"`
	TargetName   string    `json:"target_name_at_event"`
	Kind         string    `json:"kind"`
	Reason       string    `json:"reason,omitempty"`
	MessageID    string    `json:"message_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	RetainUntil  time.Time `json:"retain_until"`
	Seq          int64     `json:"-"`
}
type socialDisk struct {
	Relations map[string]FriendRelation `json:"relations"`
	Messages  []PrivateMessage          `json:"messages"`
	Events    []struct {
		Pair  string      `json:"pair"`
		Event SocialEvent `json:"event"`
	} `json:"events"`
}
type socialTx struct {
	db    *sql.Tx
	disk  socialDisk
	dirty bool
}

func socialPair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	raw, _ := json.Marshal([]string{a, b})
	return string(raw)
}

func (pg *PostgresStore) initSocialSchema() error {
	_, err := pg.db.Exec(`CREATE TABLE IF NOT EXISTS social_relations (pair_key TEXT PRIMARY KEY, member_a TEXT NOT NULL, member_b TEXT NOT NULL,payload JSONB NOT NULL);
 CREATE INDEX IF NOT EXISTS social_relations_member_b ON social_relations(member_b);
 CREATE INDEX IF NOT EXISTS social_relations_member_a ON social_relations(member_a);
 CREATE TABLE IF NOT EXISTS private_messages (seq BIGSERIAL PRIMARY KEY,id TEXT UNIQUE NOT NULL,sender_id TEXT NOT NULL,recipient_id TEXT NOT NULL,request_id TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL,payload JSONB NOT NULL,UNIQUE(sender_id,request_id));
 CREATE INDEX IF NOT EXISTS private_messages_pair ON private_messages(sender_id,recipient_id,seq DESC);
 CREATE INDEX IF NOT EXISTS private_messages_recipient ON private_messages(recipient_id,seq DESC);
 CREATE INDEX IF NOT EXISTS private_messages_daily ON private_messages(sender_id,created_at);
 CREATE TABLE IF NOT EXISTS social_events (seq BIGSERIAL PRIMARY KEY,id TEXT UNIQUE NOT NULL,pair_key TEXT NOT NULL,actor_id TEXT NOT NULL,kind TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL,payload JSONB NOT NULL);
 CREATE INDEX IF NOT EXISTS social_events_pair ON social_events(pair_key,seq DESC);
 CREATE INDEX IF NOT EXISTS social_events_daily ON social_events(actor_id,kind,created_at);`)
	return err
}

// 與帳號讀取共用 Store 鎖；SQL 寫入另用 advisory transaction lock 保護跨連線的配額／關係檢查。
// 不使用指向 Registry 的 ON DELETE CASCADE，刪除帳號不刪除追溯資料。
func (s *Store) socialTransaction(write bool, fn func(*socialTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &socialTx{}
	if s.pg != nil {
		db, err := s.pg.db.Begin()
		if err != nil {
			return err
		}
		tx.db = db
		defer db.Rollback()
		if write {
			if _, err := db.Exec(`SELECT pg_advisory_xact_lock(1398031171)`); err != nil {
				return err
			}
		}
		if err := fn(tx); err != nil {
			return err
		}
		return db.Commit()
	}
	tx.disk.Relations = map[string]FriendRelation{}
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "social_private.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		// 只有檔案不存在才可初始化；既有但不完整的快照不能覆寫。
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("私訊快照損壞: %w", err)
		}
		for _, name := range []string{"relations", "messages", "events"} {
			if _, ok := fields[name]; !ok {
				return fmt.Errorf("私訊快照缺少 %s，停止讀寫", name)
			}
		}
		if err := json.Unmarshal(raw, &tx.disk); err != nil {
			return err
		}
		if tx.disk.Relations == nil {
			return fmt.Errorf("私訊快照關係資料無效，停止讀寫")
		}
	}
	for i := range tx.disk.Messages {
		tx.disk.Messages[i].Seq = int64(i + 1)
	}
	for i := range tx.disk.Events {
		tx.disk.Events[i].Event.Pair = tx.disk.Events[i].Pair
		tx.disk.Events[i].Event.Seq = int64(i + 1)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if tx.dirty {
		raw, err := json.Marshal(tx.disk)
		if err != nil {
			return err
		}
		return writeViewSnapshot(filepath.Join(s.dataDir, "social_private.json"), raw)
	}
	return nil
}
func (t *socialTx) relation(a, b string) (FriendRelation, error) {
	key := socialPair(a, b)
	if t.db == nil {
		return t.disk.Relations[key], nil
	}
	var raw []byte
	err := t.db.QueryRow(`SELECT payload FROM social_relations WHERE pair_key=$1`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return FriendRelation{}, nil
	}
	if err != nil {
		return FriendRelation{}, err
	}
	var r FriendRelation
	err = json.Unmarshal(raw, &r)
	return r, err
}
func (t *socialTx) saveRelation(r FriendRelation) error {
	key := socialPair(r.A, r.B)
	if t.db == nil {
		t.disk.Relations[key] = r
		t.dirty = true
		return nil
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = t.db.Exec(`INSERT INTO social_relations(pair_key,member_a,member_b,payload) VALUES($1,$2,$3,$4) ON CONFLICT(pair_key) DO UPDATE SET payload=EXCLUDED.payload`, key, r.A, r.B, string(raw))
	return err
}
func (t *socialTx) relations(client string) ([]FriendRelation, error) {
	out := []FriendRelation{}
	if t.db == nil {
		for _, r := range t.disk.Relations {
			if r.A == client || r.B == client {
				out = append(out, r)
			}
		}
		return out, nil
	}
	rows, err := t.db.Query(`SELECT payload FROM social_relations WHERE member_a=$1 OR member_b=$1`, client)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var r FriendRelation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (t *socialTx) appendEvent(e SocialEvent) error {
	if t.db == nil {
		e.Seq = int64(len(t.disk.Events) + 1)
		t.disk.Events = append(t.disk.Events, struct {
			Pair  string      `json:"pair"`
			Event SocialEvent `json:"event"`
		}{e.Pair, e})
		t.dirty = true
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = t.db.Exec(`INSERT INTO social_events(id,pair_key,actor_id,kind,created_at,payload) VALUES($1,$2,$3,$4,$5,$6)`, e.ID, e.Pair, e.Actor, e.Kind, e.CreatedAt, string(raw))
	return err
}
func (t *socialTx) findMessage(sender, request string) (PrivateMessage, bool, error) {
	if t.db == nil {
		for _, m := range t.disk.Messages {
			if m.Sender == sender && m.RequestID == request {
				return m, true, nil
			}
		}
		return PrivateMessage{}, false, nil
	}
	var raw []byte
	var m PrivateMessage
	err := t.db.QueryRow(`SELECT payload,seq FROM private_messages WHERE sender_id=$1 AND request_id=$2`, sender, request).Scan(&raw, &m.Seq)
	if err == sql.ErrNoRows {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	err = json.Unmarshal(raw, &m)
	return m, err == nil, err
}
func (t *socialTx) appendMessage(m PrivateMessage) (PrivateMessage, error) {
	if t.db == nil {
		m.Seq = int64(len(t.disk.Messages) + 1)
		t.disk.Messages = append(t.disk.Messages, m)
		t.dirty = true
		return m, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	err = t.db.QueryRow(`INSERT INTO private_messages(id,sender_id,recipient_id,request_id,created_at,payload) VALUES($1,$2,$3,$4,$5,$6) RETURNING seq`, m.ID, m.Sender, m.Recipient, m.RequestID, m.CreatedAt, string(raw)).Scan(&m.Seq)
	return m, err
}
func (t *socialTx) countDaily(actor, kind string, start time.Time) (int, error) {
	count := 0
	if t.db == nil {
		for _, e := range t.disk.Events {
			if e.Event.Actor == actor && e.Event.Kind == kind && !e.Event.CreatedAt.Before(start) {
				count++
			}
		}
		return count, nil
	}
	err := t.db.QueryRow(`SELECT COUNT(*) FROM social_events WHERE actor_id=$1 AND kind=$2 AND created_at >= $3`, actor, kind, start).Scan(&count)
	return count, err
}
func (t *socialTx) messages(a, b, before string, limit int) ([]PrivateMessage, bool, error) {
	out := []PrivateMessage{}
	cursor := int64(9223372036854775807)
	if t.db == nil {
		if before != "" {
			found := false
			for _, m := range t.disk.Messages {
				if m.ID == before && socialPair(m.Sender, m.Recipient) == socialPair(a, b) {
					cursor = m.Seq
					found = true
					break
				}
			}
			if !found {
				return nil, false, fmt.Errorf("私訊游標不屬於此對話")
			}
		}
		for _, m := range t.disk.Messages {
			if socialPair(m.Sender, m.Recipient) == socialPair(a, b) && m.Seq < cursor {
				out = append(out, m)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	} else {
		if before != "" {
			if err := t.db.QueryRow(`SELECT seq FROM private_messages WHERE id=$1 AND ((sender_id=$2 AND recipient_id=$3) OR(sender_id=$3 AND recipient_id=$2))`, before, a, b).Scan(&cursor); err != nil {
				return nil, false, fmt.Errorf("私訊游標無效")
			}
		}
		rows, err := t.db.Query(`SELECT payload,seq FROM private_messages WHERE ((sender_id=$1 AND recipient_id=$2) OR(sender_id=$2 AND recipient_id=$1)) AND seq<$3 ORDER BY seq DESC LIMIT $4`, a, b, cursor, limit+1)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var m PrivateMessage
			if err := rows.Scan(&raw, &m.Seq); err != nil {
				return nil, false, err
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, false, err
			}
			out = append(out, m)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
func (t *socialTx) history(a, b, before string, limit int) ([]SocialEvent, bool, error) {
	out := []SocialEvent{}
	key := socialPair(a, b)
	cursor := int64(9223372036854775807)
	if t.db == nil {
		if before != "" {
			found := false
			for _, item := range t.disk.Events {
				e := item.Event
				if e.ID == before && e.Pair == key && e.Kind != "admin_read_messages" {
					cursor = e.Seq
					found = true
				}
			}
			if !found {
				return nil, false, fmt.Errorf("紀錄游標無效")
			}
		}
		for i := len(t.disk.Events) - 1; i >= 0 && len(out) <= limit; i-- {
			e := t.disk.Events[i].Event
			if e.Pair == key && e.Kind != "admin_read_messages" && e.Seq < cursor {
				out = append(out, e)
			}
		}
	} else {
		if before != "" {
			if err := t.db.QueryRow(`SELECT seq FROM social_events WHERE id=$1 AND pair_key=$2 AND kind<>'admin_read_messages'`, before, key).Scan(&cursor); err != nil {
				return nil, false, fmt.Errorf("紀錄游標無效")
			}
		}
		rows, err := t.db.Query(`SELECT payload,seq FROM social_events WHERE pair_key=$1 AND kind<>'admin_read_messages' AND seq<$2 ORDER BY seq DESC LIMIT $3`, key, cursor, limit+1)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var e SocialEvent
			if err := rows.Scan(&raw, &e.Seq); err != nil {
				return nil, false, err
			}
			if err := json.Unmarshal(raw, &e); err != nil {
				return nil, false, err
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
func (t *socialTx) conversations(client, before string, limit int) ([]PrivateMessage, bool, error) {
	out := []PrivateMessage{}
	cursor := int64(9223372036854775807)
	if t.db == nil {
		if before != "" {
			found := false
			for _, m := range t.disk.Messages {
				if m.ID == before && (m.Sender == client || m.Recipient == client) {
					cursor = m.Seq
					found = true
				}
			}
			if !found {
				return nil, false, fmt.Errorf("對話游標無效")
			}
		}
		latest := map[string]PrivateMessage{}
		for _, m := range t.disk.Messages {
			peer := ""
			if m.Sender == client {
				peer = m.Recipient
			} else if m.Recipient == client {
				peer = m.Sender
			}
			if peer != "" && m.Seq > latest[peer].Seq {
				latest[peer] = m
			}
		}
		for _, m := range latest {
			if m.Seq < cursor {
				out = append(out, m)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	} else {
		if before != "" {
			if err := t.db.QueryRow(`SELECT seq FROM private_messages WHERE id=$1 AND (sender_id=$2 OR recipient_id=$2)`, before, client).Scan(&cursor); err != nil {
				return nil, false, fmt.Errorf("對話游標無效")
			}
		}
		rows, err := t.db.Query(`SELECT payload,seq FROM (SELECT DISTINCT ON (CASE WHEN sender_id=$1 THEN recipient_id ELSE sender_id END) payload,seq FROM private_messages WHERE sender_id=$1 OR recipient_id=$1 ORDER BY CASE WHEN sender_id=$1 THEN recipient_id ELSE sender_id END,seq DESC) latest WHERE seq<$2 ORDER BY seq DESC LIMIT $3`, client, cursor, limit+1)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var m PrivateMessage
			if err := rows.Scan(&raw, &m.Seq); err != nil {
				return nil, false, err
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, false, err
			}
			out = append(out, m)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
