package main

import (
	"fmt"
	"github.com/rs/xid"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func chatRequestID(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func chatRecord(room, actor, name, kind, target, text, request string) ChatRecord {
	now := time.Now()
	return ChatRecord{ID: xid.New().String(), RoomID: room, Actor: actor, ActorName: name, Kind: kind, Target: target, Text: text, RequestID: request, CreatedAt: now, RetainUntil: now.AddDate(0, 6, 0)}
}
func chatJoinMode(r AgentChatroom) string {
	if r.JoinMode == "public" {
		return "public"
	}
	return "invite"
}
func chatView(r AgentChatroom, client string) map[string]any {
	return map[string]any{"collaboration": r.Collaboration, "ready": r.Sandbox != nil && r.Sandbox.Ready, "room_id": r.ID, "name": r.Name, "join_mode": chatJoinMode(r), "owner_id": r.Owner, "status": r.Status, "membership": r.Members[client], "created_at": r.CreatedAt, "closed_at": r.ClosedAt, "last_seq": r.LastSeq}
}
func (s *Store) chatAccessLocked(r AgentChatroom, client string, write bool) error {
	if _, err := s.socialAccountLocked(client, write); err != nil {
		return err
	}
	if r.Status == "closed" && client != r.Owner {
		return ErrForbidden
	}
	if client != r.Owner && r.Members[client] != "joined" {
		return ErrForbidden
	}
	if write && r.Status != "open" {
		return fmt.Errorf("聊天室已關閉，不能新增互動")
	}
	return nil
}
func (s *Store) CreateChatroom(client, name, request string) (map[string]any, error) {
	return s.CreateChatroomWithMode(client, name, request, "invite")
}
func (s *Store) CreateChatroomWithMode(client, name, request, mode string) (map[string]any, error) {
	return s.createChatroomKind(client, name, request, mode, false)
}
func (s *Store) createChatroomKind(client, name, request, mode string, collaboration bool) (map[string]any, error) {
	if mode == "" {
		mode = "invite"
	}
	if mode != "invite" && mode != "public" {
		return nil, fmt.Errorf("join_mode須為invite或public")
	}
	name = strings.TrimSpace(name)
	if !chatRequestID(request) || !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("名稱須為1至80字元；request_id必填且最多128 bytes，不含空白或控制字元")
	}
	var result map[string]any
	err := s.socialTransaction(true, func(tx *socialTx) error {
		if collaboration {
			enabled, e := tx.collaborationEnabled()
			if e != nil {
				return e
			}
			if !enabled || s.collaborationDir == "" {
				return fmt.Errorf("共同協作尚未啟用")
			}
		}
		a, err := s.socialAccountLocked(client, true)
		if err != nil {
			return err
		}
		r, found, err := tx.chatCreated(client, request)
		if err != nil {
			return err
		}
		if found {
			originalName := r.InitialName
			if originalName == "" {
				originalName = r.Name
			}
			if originalName != name || chatJoinMode(r) != mode || r.Collaboration != collaboration {
				return fmt.Errorf("request_id已用於不同聊天室名稱或加入模式")
			}
			result = chatView(r, client)
			result["duplicate"] = true
			return nil
		}
		if s.chatArchiveDir == "" {
			return fmt.Errorf("聊天室匯出尚未設定")
		}
		rooms, err := tx.chatRooms(client)
		if err != nil {
			return err
		}
		open := 0
		for _, r := range rooms {
			if r.Owner == client && r.Status == "open" {
				open++
			}
		}
		if open >= 100 {
			return fmt.Errorf("同時開啟的聊天室最多100間")
		}
		r = AgentChatroom{Collaboration: collaboration, InitialName: name, JoinMode: mode, ID: xid.New().String(), Owner: client, OwnerName: a.DisplayName, Name: name, RequestID: request, Status: "open", Members: map[string]string{client: "joined"}, CreatedAt: time.Now()}
		if collaboration {
			r.Sandbox = &CollaborationSandbox{Files: map[string]CollaborationFile{}}
		}
		r.Archive = ChatArchive{Directory: filepath.Join(s.chatArchiveDir, r.ID), Filename: chatArchiveFilename}
		if err := tx.appendChat(&r, chatRecord(r.ID, client, a.DisplayName, "opened", "", "", "")); err != nil {
			return err
		}
		result = chatView(r, client)
		result["duplicate"] = false
		return nil
	})
	return result, err
}
func (s *Store) ManageChatroom(client, id, action, peer string) (map[string]any, error) {
	var result map[string]any
	err := s.socialTransaction(true, func(tx *socialTx) error {
		a, err := s.socialAccountLocked(client, true)
		if err != nil {
			return err
		}
		r, err := tx.chatRoom(id)
		if err != nil {
			return err
		}
		if action == "close" && client == r.Owner && r.Status == "closed" {
			result = chatView(r, client)
			return nil
		}
		if r.Status != "open" {
			return fmt.Errorf("聊天室已關閉，不能重新開啟或更動成員")
		}
		if r.Collaboration && (r.Sandbox == nil || !r.Sandbox.Ready) && action != "publish" && action != "close" {
			return fmt.Errorf("發起者須先上傳初始檔案並發布協作空間")
		}
		target := peer
		noop := false
		switch action {
		case "publish":
			if !r.Collaboration || client != r.Owner || r.Sandbox == nil {
				return ErrForbidden
			}
			hasFile := false
			for _, f := range r.Sandbox.Files {
				if len(f.Versions) > 0 {
					hasFile = true
					break
				}
			}
			if !hasFile {
				return fmt.Errorf("請先上傳初始協作檔案")
			}
			if r.Sandbox.Ready {
				noop = true
			} else {
				r.Sandbox.Ready = true
			}
			target = ""
		case "join":
			target = client
			if chatJoinMode(r) != "public" {
				return fmt.Errorf("此聊天室須由發起者邀請好友")
			}
			if r.Members[client] == "removed" {
				return fmt.Errorf("已被移除，需發起者重新邀請")
			}
			if client != r.Owner {
				if _, err := s.socialAccountLocked(r.Owner, false); err != nil {
					return ErrForbidden
				}
				relation, err := tx.relation(client, r.Owner)
				if err != nil {
					return err
				}
				if relation.BlockedA || relation.BlockedB {
					return ErrForbidden
				}
			}
			if r.Members[client] == "joined" {
				noop = true
				break
			}
			if len(r.Members) >= 100 && r.Members[client] == "" {
				return fmt.Errorf("每個聊天室最多100個不同帳號")
			}
			r.Members[client] = "joined"

		case "invite":
			if client != r.Owner {
				return ErrForbidden
			}
			if peer == "" || peer == client {
				return fmt.Errorf("請指定其他好友ID")
			}
			if _, err := s.socialAccountLocked(peer, false); err != nil {
				return ErrForbidden
			}
			rel, err := tx.relation(client, peer)
			if err != nil {
				return err
			}
			if rel.Status != "accepted" || rel.BlockedA || rel.BlockedB {
				return fmt.Errorf("僅能邀請未封鎖的好友")
			}
			if r.Members[peer] == "joined" || r.Members[peer] == "invited" {
				noop = true
			} else {
				if len(r.Members) >= 100 && r.Members[peer] == "" {
					return fmt.Errorf("每個聊天室最多100個不同帳號")
				}
				r.Members[peer] = "invited"
			}
		case "accept", "decline":
			target = client
			if action == "accept" && r.Members[client] == "joined" {
				noop = true
				break
			}
			if action == "decline" && r.Members[client] == "declined" {
				noop = true
				break
			}
			if r.Members[client] != "invited" {
				return ErrForbidden
			}
			if action == "accept" {
				if _, err := s.socialAccountLocked(r.Owner, false); err != nil {
					return ErrForbidden
				}
				rel, err := tx.relation(client, r.Owner)
				if err != nil {
					return err
				}
				if rel.Status != "accepted" || rel.BlockedA || rel.BlockedB {
					return fmt.Errorf("接受邀請時仍須為發起者的好友")
				}
				r.Members[client] = "joined"
			} else {
				r.Members[client] = "declined"
			}
		case "leave":
			target = client
			if client == r.Owner {
				return fmt.Errorf("發起者請使用close關閉聊天室")
			}
			if r.Members[client] == "left" {
				noop = true
				break
			}
			if r.Members[client] != "joined" {
				return ErrForbidden
			}
			r.Members[client] = "left"
		case "remove":
			if client != r.Owner || peer == "" || peer == client {
				return ErrForbidden
			}
			if r.Members[peer] == "removed" {
				noop = true
				break
			}
			if r.Members[peer] != "joined" && r.Members[peer] != "invited" {
				return ErrForbidden
			}
			r.Members[peer] = "removed"
		case "close":
			if client != r.Owner {
				return ErrForbidden
			}
			r.Status = "closed"
			r.ClosedAt = time.Now()
			target = ""
		default:
			return fmt.Errorf("不支援的聊天室操作")
		}
		if !noop && r.Sandbox != nil && (action == "leave" || action == "remove" || action == "close") {
			for p, f := range r.Sandbox.Files {
				if f.Lock != nil && (action == "close" || f.Lock.Owner == target) {
					f.Lock = nil
					if len(f.Versions) == 0 {
						delete(r.Sandbox.Files, p)
					} else {
						r.Sandbox.Files[p] = f
					}
				}
			}
		}
		if !noop {
			if err := tx.appendChat(&r, chatRecord(id, client, a.DisplayName, action, target, "", "")); err != nil {
				return err
			}
		}
		result = chatView(r, client)
		result["unchanged"] = noop
		if action == "close" {
			delete(s.chatPresence, id)
		} else if action == "leave" || action == "remove" {
			delete(s.chatPresence[id], target)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if action == "close" {
		// 關閉先提交；匯出失敗不重新開放，背景工作會重試。
		if e := s.ExportChatroom(id); e != nil {
			result["archive_pending"] = true
			result["archive_notice"] = "聊天室已關閉，匯出待背景重試"
		} else {
			result["archive_pending"] = false
		}
	}
	return result, nil
}
func (s *Store) SendChatMessage(client, id, text, request string) (map[string]any, error) {
	if !chatRequestID(request) || !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 8000 || strings.ContainsRune(text, 0) {
		return nil, fmt.Errorf("訊息須為1至8000字元且不可含NUL；request_id必填、最多128 bytes")
	}
	var result map[string]any
	err := s.socialTransaction(true, func(tx *socialTx) error {
		r, err := tx.chatRoom(id)
		if err != nil {
			return err
		}
		if err := s.chatAccessLocked(r, client, false); err != nil {
			return err
		}
		if _, err := s.socialAccountLocked(client, true); err != nil {
			return err
		}
		existing, found, err := tx.chatRetry(id, client, request)
		if err != nil {
			return err
		}
		if found {
			if existing.Text != text {
				return fmt.Errorf("request_id已用於不同訊息")
			}
			result = map[string]any{"record": existing, "duplicate": true}
			return nil
		}
		if r.Status != "open" {
			return fmt.Errorf("聊天室已關閉")
		}
		record := chatRecord(id, client, s.agentRegistry[client].DisplayName, "message", "", text, request)
		if err := tx.appendChat(&r, record); err != nil {
			return err
		}
		record.Seq = r.LastSeq
		result = map[string]any{"record": record, "duplicate": false}
		return nil
	})
	return result, err
}
func (s *Store) ReadChatroom(client, id string, after int64, limit int) (map[string]any, error) {
	if after < 0 {
		return nil, fmt.Errorf("after_seq不可為負數")
	}
	var result map[string]any
	limit = socialLimit(limit)
	err := s.socialTransaction(false, func(tx *socialTx) error {
		r, err := tx.chatRoom(id)
		if err != nil {
			return err
		}
		if err := s.chatAccessLocked(r, client, false); err != nil {
			return err
		}
		records, err := tx.chatRecords(id, after, limit+1)
		if err != nil {
			return err
		}
		more := len(records) > limit
		if more {
			records = records[:limit]
		}
		next := after
		if len(records) > 0 {
			next = records[len(records)-1].Seq
		}
		result = chatView(r, client)
		result["records"] = records
		result["has_more"] = more
		result["next_after_seq"] = next
		result["members"] = r.Members
		return nil
	})
	return result, err
}
func (s *Store) ListChatrooms(client, after string, limit int) (map[string]any, error) {
	return s.ListChatroomsScope(client, after, limit, "mine")
}
func (s *Store) ListChatroomsScope(client, after string, limit int, scope string) (map[string]any, error) {
	if scope == "" {
		scope = "mine"
	}
	if scope != "mine" && scope != "public" && scope != "collaborations" && scope != "public_collaborations" {
		return nil, fmt.Errorf("scope須為mine/public/collaborations/public_collaborations")
	}
	var result map[string]any
	limit = socialLimit(limit)
	err := s.socialTransaction(false, func(tx *socialTx) error {
		if _, err := s.socialAccountLocked(client, false); err != nil {
			return err
		}
		queryClient := client
		if scope == "public" || scope == "public_collaborations" {
			queryClient = ""
		}
		rooms, err := tx.chatRooms(queryClient)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		for _, r := range rooms {
			state := r.Members[client]
			visible := r.Owner == client || (r.Status == "open" && (state == "joined" || state == "invited"))
			if scope == "public" || scope == "public_collaborations" {
				visible = r.Status == "open" && chatJoinMode(r) == "public"
			}
			wantCollaboration := scope == "collaborations" || scope == "public_collaborations"
			if r.Collaboration != wantCollaboration {
				visible = false
			}
			if r.Collaboration {
				enabled, e := tx.collaborationEnabled()
				if e != nil {
					return e
				}
				if !enabled {
					visible = false
				}
				if (r.Sandbox == nil || !r.Sandbox.Ready) && client != r.Owner {
					visible = false
				}
			}
			if r.ID > after && visible {
				out = append(out, chatView(r, client))
			}
		}
		more := len(out) > limit
		if more {
			out = out[:limit]
		}
		next := ""
		if more {
			next = out[len(out)-1]["room_id"].(string)
		}
		result = map[string]any{"chatrooms": out, "has_more": more, "next_after_id": next}
		return nil
	})
	return result, err
}

// 改名只改顯示名稱；穩定 ID、成員、加入模式及匯出位置均不變。
func (s *Store) RenameChatroom(client, id, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("聊天室名稱須為1至80字元且不含控制字元")
	}
	var result map[string]any
	err := s.socialTransaction(true, func(tx *socialTx) error {
		account, err := s.socialAccountLocked(client, true)
		if err != nil {
			return err
		}
		room, err := tx.chatRoom(id)
		if err != nil {
			return err
		}
		if room.Owner != client {
			return ErrForbidden
		}
		if room.Status != "open" {
			return fmt.Errorf("聊天室已關閉，不能更換名稱")
		}
		unchanged := room.Name == name
		if !unchanged {
			if room.InitialName == "" {
				room.InitialName = room.Name
			}
			record := chatRecord(id, client, account.DisplayName, "rename", "", "", "")
			record.OldName = room.Name
			record.NewName = name
			room.Name = name
			if err := tx.appendChat(&room, record); err != nil {
				return err
			}
		}
		result = chatView(room, client)
		result["unchanged"] = unchanged
		return nil
	})
	return result, err
}
