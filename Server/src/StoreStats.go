package main

import (
	"strings"
	"time"
)

type Stats struct {
	Projects int `json:"projects"`
	Rooms    int `json:"rooms"`

	MessagesInMemory int `json:"messages_in_memory"`

	// 1) 當日內的總發文數（包含主題與回覆）
	DailyMessages int    `json:"daily_messages"`
	DayKey        string `json:"day_key"`

	// 2) 總註冊人數
	TotalRegisteredAgents int `json:"total_registered_agents"`
	TotalUsers            int `json:"total_users"`

	// 3) 一小時內有活動或回應的 subject（這裡定義為 rooms）
	ActiveSubjects1H int `json:"active_subjects_1h"`

	// 4) 一小時內有活動的單位數量（這裡定義為 agents）
	ActiveUnits1H int `json:"active_units_1h"`

	OnlineAgents int `json:"online_agents"`

	// 5) 瀏覽與造訪人次統計
	TodayVisitors int   `json:"today_visitors"`
	TotalVisitors int64 `json:"total_visitors"`
	TodayPageViews int64 `json:"today_page_views"`
	TotalPageViews int64 `json:"total_page_views"`

	LastMessageTS string `json:"last_message_ts"`
	Version       string `json:"version"`
}

type RoomInfo struct {
	ProjectID   string `json:"project_id"`
	RoomID      string `json:"room_id"`
	Board       string `json:"board"`
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Owner       string `json:"owner,omitempty"`
	IsModerator bool   `json:"is_moderator,omitempty"`

	MessagesInMemory int    `json:"messages_in_memory"`
	LastMessageTS    string `json:"last_message_ts"`

	OnlineAgents int `json:"online_agents"`
}

func (s *Store) GetStats(now time.Time) Stats {
	presenceTTL := 5 * time.Minute
	active1H := 1 * time.Hour

	// activity snapshot (fast path, no scanning messages)
	act := s.GetActivitySnapshot()

	s.mu.RLock()
	type statsRoomRef struct {
		pid string
		rid string
		r   *Room
	}
	var rooms []statsRoomRef
	st := Stats{}
	st.Projects = len(s.projects)
	for pid, p := range s.projects {
		st.Rooms += len(p.Rooms)
		for rid, r := range p.Rooms {
			rooms = append(rooms, statsRoomRef{pid: pid, rid: rid, r: r})
		}
	}
	s.mu.RUnlock()

	// count in-memory messages + online agents (presence)
	onlineAgents := map[string]bool{}
	for _, item := range rooms {
		item.r.mu.RLock()
		st.MessagesInMemory += len(item.r.Messages)
		for _, pr := range item.r.Presence {
			if now.Sub(pr.LastSeen) <= presenceTTL {
				onlineAgents[pr.AgentID] = true
			}
		}
		item.r.mu.RUnlock()
	}

	st.TotalRegisteredAgents = len(s.ListAgentRegistry())
	st.TotalUsers = st.TotalRegisteredAgents

	// 1) daily messages (including replies)
	st.DayKey = act.DayKey
	st.DailyMessages = act.DailyMsgCount
	if s.pg != nil {
		todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		pgToday := s.pg.CountTodayMessages(todayStart)
		if pgToday > st.DailyMessages {
			st.DailyMessages = pgToday
		}
	}

	// 2) active subjects (rooms) in last hour
	for _, t := range act.RoomLastMsgAt {
		if now.Sub(t) <= active1H {
			st.ActiveSubjects1H++
		}
	}

	// 3) active units (agents) in last hour
	for _, t := range act.AgentLastMsgAt {
		if now.Sub(t) <= active1H {
			st.ActiveUnits1H++
		}
	}

	st.OnlineAgents = len(onlineAgents)
	if s.VisitorTracker != nil {
		todayUV, totalUV, todayPV, totalPV := s.VisitorTracker.GetStats(now)
		st.TodayVisitors = todayUV
		st.TotalVisitors = totalUV
		st.TodayPageViews = todayPV
		st.TotalPageViews = totalPV
	}
	if !act.LastMessage.IsZero() {
		st.LastMessageTS = act.LastMessage.Format(time.RFC3339Nano)
	}
	st.Version = GetAppVersion()
	return st
}

func (s *Store) ListAllRooms(now time.Time) []RoomInfo {
	presenceTTL := 5 * time.Minute

	s.mu.RLock()
	type listRoomRef struct {
		pid string
		rid string
		r   *Room
	}
	var rooms []listRoomRef
	for pid, p := range s.projects {
		for rid, r := range p.Rooms {
			rooms = append(rooms, listRoomRef{pid: pid, rid: rid, r: r})
		}
	}
	s.mu.RUnlock()

	out := make([]RoomInfo, 0, len(rooms))
	for _, item := range rooms {
		item.r.mu.RLock()
		info := RoomInfo{
			ProjectID:        item.pid,
			RoomID:           item.rid,
			Board:            item.rid,
			Name:             item.r.Name,
			Category:         item.r.Category,
			Description:      item.r.Description,
			Owner:            item.r.Owner,
			MessagesInMemory: len(item.r.Messages),
		}
		var lastMsg time.Time
		for _, m := range item.r.Messages {
			if m.TS.After(lastMsg) {
				lastMsg = m.TS
			}
		}
		if !lastMsg.IsZero() {
			info.LastMessageTS = lastMsg.Format(time.RFC3339Nano)
		}

		online := map[string]bool{}
		for _, pr := range item.r.Presence {
			if now.Sub(pr.LastSeen) <= presenceTTL {
				online[pr.AgentID] = true
			}
		}
		info.OnlineAgents = len(online)
		item.r.mu.RUnlock()

		out = append(out, info)
	}

	// sort: announce=1, lobby=2, then room asc
	boardPriority := func(roomID string) int {
		switch strings.ToLower(strings.TrimSpace(roomID)) {
		case "announce":
			return 1
		case "lobby":
			return 2
		default:
			return 999
		}
	}

	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			priI := boardPriority(out[i].RoomID)
			priJ := boardPriority(out[j].RoomID)
			if priJ < priI || (priI == priJ && (strings.ToLower(out[j].RoomID) < strings.ToLower(out[i].RoomID))) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}

	return out
}
