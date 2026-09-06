package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type ChatArchive struct {
	Directory  string    `json:"directory"`
	Filename   string    `json:"filename"`
	SHA256     string    `json:"sha256"`
	ThroughSeq int64     `json:"through_seq"`
	Records    int64     `json:"records"`
	ExportedAt time.Time `json:"exported_at"`
}
type AgentChatroom struct {
	InitialName string            `json:"name_at_creation,omitempty"`
	JoinMode    string            `json:"join_mode,omitempty"`
	ID          string            `json:"id"`
	Owner       string            `json:"owner_id"`
	OwnerName   string            `json:"owner_name_at_creation"`
	Name        string            `json:"name"`
	RequestID   string            `json:"request_id"`
	Status      string            `json:"status"`
	Members     map[string]string `json:"members"`
	CreatedAt   time.Time         `json:"created_at"`
	ClosedAt    time.Time         `json:"closed_at,omitempty"`
	LastSeq     int64             `json:"last_seq"`
	Archive     ChatArchive       `json:"archive"`
}
type ChatRecord struct {
	OldName     string    `json:"old_name,omitempty"`
	NewName     string    `json:"new_name,omitempty"`
	ID          string    `json:"id"`
	RoomID      string    `json:"room_id"`
	Seq         int64     `json:"seq"`
	Kind        string    `json:"kind"`
	Actor       string    `json:"actor_id"`
	ActorName   string    `json:"actor_name_at_event"`
	Target      string    `json:"target_id,omitempty"`
	Text        string    `json:"text,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	RetainUntil time.Time `json:"retain_until"`
}

func (pg *PostgresStore) initChatSchema() error {
	_, err := pg.db.Exec(`CREATE TABLE IF NOT EXISTS agent_chatrooms(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,request_id TEXT NOT NULL,payload JSONB NOT NULL,UNIQUE(owner_id,request_id));
 CREATE INDEX IF NOT EXISTS agent_chatrooms_members ON agent_chatrooms USING gin ((payload->'members'));
 CREATE TABLE IF NOT EXISTS agent_chat_records(room_id TEXT NOT NULL,seq BIGINT NOT NULL,id TEXT UNIQUE NOT NULL,actor_id TEXT NOT NULL,request_id TEXT, payload JSONB NOT NULL,PRIMARY KEY(room_id,seq),UNIQUE(room_id,actor_id,request_id));`)
	return err
}
func (t *socialTx) chatRoom(id string) (AgentChatroom, error) {
	if t.db == nil {
		r, ok := t.disk.Chatrooms[id]
		if !ok {
			return r, fmt.Errorf("聊天室不存在")
		}
		return r, nil
	}
	var raw []byte
	err := t.db.QueryRow(`SELECT payload FROM agent_chatrooms WHERE id=$1`, id).Scan(&raw)
	if err != nil {
		return AgentChatroom{}, err
	}
	var r AgentChatroom
	err = json.Unmarshal(raw, &r)
	return r, err
}
func (t *socialTx) chatRooms(client string) ([]AgentChatroom, error) {
	out := []AgentChatroom{}
	if t.db == nil {
		for _, r := range t.disk.Chatrooms {
			if client == "" || r.Owner == client || r.Members[client] != "" {
				out = append(out, r)
			}
		}
	} else {
		var rows *sql.Rows
		var err error
		if client == "" {
			rows, err = t.db.Query(`SELECT payload FROM agent_chatrooms ORDER BY id`)
		} else {
			rows, err = t.db.Query(`SELECT payload FROM agent_chatrooms WHERE owner_id=$1 OR (payload->'members') ? $1 ORDER BY id`, client)
		}
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var r AgentChatroom
			if err := rows.Scan(&raw); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (t *socialTx) chatCreated(owner, request string) (AgentChatroom, bool, error) {
	if t.db == nil {
		for _, r := range t.disk.Chatrooms {
			if r.Owner == owner && r.RequestID == request {
				return r, true, nil
			}
		}
		return AgentChatroom{}, false, nil
	}
	var raw []byte
	err := t.db.QueryRow(`SELECT payload FROM agent_chatrooms WHERE owner_id=$1 AND request_id=$2`, owner, request).Scan(&raw)
	if err == sql.ErrNoRows {
		return AgentChatroom{}, false, nil
	}
	if err != nil {
		return AgentChatroom{}, false, err
	}
	var r AgentChatroom
	err = json.Unmarshal(raw, &r)
	return r, true, err
}
func (t *socialTx) saveChat(r AgentChatroom) error {
	if t.db == nil {
		if t.disk.Chatrooms == nil {
			t.disk.Chatrooms = map[string]AgentChatroom{}
		}
		t.disk.Chatrooms[r.ID] = r
		t.dirty = true
		return nil
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = t.db.Exec(`INSERT INTO agent_chatrooms(id,owner_id,request_id,payload) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET payload=EXCLUDED.payload`, r.ID, r.Owner, r.RequestID, string(raw))
	return err
}
func (t *socialTx) appendChat(r *AgentChatroom, record ChatRecord) error {
	r.LastSeq++
	record.Seq = r.LastSeq
	if t.db == nil {
		t.disk.ChatRecords = append(t.disk.ChatRecords, record)
		t.dirty = true
	} else {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		var req any
		if record.RequestID != "" {
			req = record.RequestID
		}
		if _, err = t.db.Exec(`INSERT INTO agent_chat_records(room_id,seq,id,actor_id,request_id,payload) VALUES($1,$2,$3,$4,$5,$6)`, r.ID, record.Seq, record.ID, record.Actor, req, string(raw)); err != nil {
			return err
		}
	}
	return t.saveChat(*r)
}
func (t *socialTx) chatRetry(room, actor, request string) (ChatRecord, bool, error) {
	if t.db == nil {
		for _, r := range t.disk.ChatRecords {
			if r.RoomID == room && r.Actor == actor && r.RequestID == request {
				return r, true, nil
			}
		}
		return ChatRecord{}, false, nil
	}
	var raw []byte
	err := t.db.QueryRow(`SELECT payload FROM agent_chat_records WHERE room_id=$1 AND actor_id=$2 AND request_id=$3`, room, actor, request).Scan(&raw)
	if err == sql.ErrNoRows {
		return ChatRecord{}, false, nil
	}
	if err != nil {
		return ChatRecord{}, false, err
	}
	var r ChatRecord
	err = json.Unmarshal(raw, &r)
	return r, true, err
}
func (t *socialTx) chatRecords(room string, after int64, limit int) ([]ChatRecord, error) {
	out := []ChatRecord{}
	if t.db == nil {
		for _, r := range t.disk.ChatRecords {
			if r.RoomID == room && r.Seq > after {
				out = append(out, r)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
		if len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	}
	rows, err := t.db.Query(`SELECT payload FROM agent_chat_records WHERE room_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, room, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var r ChatRecord
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
