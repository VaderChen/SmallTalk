package main

import (
	"github.com/rs/xid"
	"net/http"
	"strconv"
	"time"
)

// 公開瀏覽只輸出允許公開的欄位；不可直接序列化內部聊天室或紀錄。
func (s *Store) publicChatView(r AgentChatroom) map[string]any {
	return map[string]any{"active_participant_count": s.chatPresenceCountLocked(r, time.Now()), "join_mode": chatJoinMode(r), "room_id": r.ID, "name": r.Name, "owner_name": r.OwnerName, "status": r.Status, "created_at": r.CreatedAt}
}
func (api *BBSAPI) publicChatHTTP(w http.ResponseWriter, r *http.Request, parts []string) []byte {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, message string) []byte {
		w.WriteHeader(status)
		return mustJSON(ErrorResponse{Error: message})
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		return fail(405, "聊天專區僅供閱讀")
	}
	if len(parts) > 2 {
		return fail(404, "找不到頁面")
	}
	q := r.URL.Query()
	limit := 50
	if q.Has("limit") {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > 100 {
			return fail(400, "limit須為1至100")
		}
		limit = n
	}
	var result map[string]any
	status := 500
	err := api.getStore().socialTransaction(false, func(tx *socialTx) error {
		if len(parts) == 1 {
			rooms, e := tx.chatRooms("")
			if e != nil {
				return e
			}
			out := []map[string]any{}
			for _, room := range rooms {
				if room.Status == "open" && room.ID > q.Get("after_id") {
					out = append(out, api.getStore().publicChatView(room))
					if len(out) > limit {
						break
					}
				}
			}
			more := len(out) > limit
			if more {
				out = out[:limit]
			}
			next := ""
			if len(out) > 0 {
				next = out[len(out)-1]["room_id"].(string)
			}
			result = map[string]any{"chatrooms": out, "has_more": more, "next_after_id": next}
			return nil
		}
		if _, e := xid.FromString(parts[1]); e != nil {
			status = 404
			return e
		}
		after := int64(0)
		if q.Has("after_seq") {
			n, e := strconv.ParseInt(q.Get("after_seq"), 10, 64)
			if e != nil || n < 0 {
				status = 400
				return strconv.ErrSyntax
			}
			after = n
		}
		room, e := tx.chatRoom(parts[1])
		if e != nil {
			status = 404
			return e
		}
		if room.Status != "open" {
			status = 404
			return ErrForbidden
		}
		records, e := tx.chatRecords(room.ID, after, limit+1)
		if e != nil {
			return e
		}
		more := len(records) > limit
		if more {
			records = records[:limit]
		}
		messages := []map[string]any{}
		next := after
		for _, record := range records {
			next = record.Seq
			if record.Kind == "message" {
				messages = append(messages, map[string]any{"seq": record.Seq, "author": record.ActorName, "author_id": record.Actor, "text": record.Text, "created_at": record.CreatedAt})
			}
		}
		result = api.getStore().publicChatView(room)
		result["messages"] = messages
		result["next_after_seq"] = next
		result["has_more"] = more
		return nil
	})
	if err != nil {
		if status == 404 {
			return fail(404, "聊天室不存在")
		}
		if status == 400 {
			return fail(400, "after_seq須為非負整數")
		}
		return fail(500, "聊天室暫時無法讀取")
	}
	return mustJSON(result)
}
