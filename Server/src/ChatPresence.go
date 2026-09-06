package main

import (
	"fmt"
	"time"
)

const chatPresenceTTL = 90 * time.Second

// 心跳只存記憶體；重啟要求重新報到，不把持久名冊當作在線連線。
func (s *Store) SetChatPresence(client, id, status string) (map[string]any, error) {
	if status != "online" && status != "offline" {
		return nil, fmt.Errorf("status須為online或offline")
	}
	var result map[string]any
	err := s.socialTransaction(false, func(tx *socialTx) error {
		room, err := tx.chatRoom(id)
		if err != nil {
			return err
		}
		if err := s.chatAccessLocked(room, client, true); err != nil {
			return err
		}
		now := time.Now()
		if s.chatPresence == nil {
			s.chatPresence = map[string]map[string]time.Time{}
		}
		// 清除逾時房間，避免歷史心跳無限累積。
		for rid, members := range s.chatPresence {
			for member, seen := range members {
				if !seen.Add(chatPresenceTTL).After(now) {
					delete(members, member)
				}
			}
			if len(members) == 0 {
				delete(s.chatPresence, rid)
			}
		}
		if status == "online" {
			if s.chatPresence[id] == nil {
				s.chatPresence[id] = map[string]time.Time{}
			}
			s.chatPresence[id][client] = now
		} else {
			delete(s.chatPresence[id], client)
		}
		result = map[string]any{"ok": true, "status": status, "ttl_seconds": 90, "heartbeat_interval_seconds": 30, "active_participant_count": s.chatPresenceCountLocked(room, now)}
		return nil
	})
	return result, err
}
func (s *Store) chatPresenceCountLocked(room AgentChatroom, now time.Time) int {
	if room.Status != "open" {
		delete(s.chatPresence, room.ID)
		return 0
	}
	members := s.chatPresence[room.ID]
	count := 0
	for client, seen := range members {
		if !seen.Add(chatPresenceTTL).After(now) || room.Members[client] != "joined" {
			delete(members, client)
			continue
		}
		if _, err := s.socialAccountLocked(client, true); err != nil {
			delete(members, client)
			continue
		}
		count++
	}
	return count
}
