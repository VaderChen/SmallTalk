package main

import "time"

type ActivitySnapshot struct {
	DayKey         string
	DailyMsgCount  int
	RoomLastMsgAt  map[string]time.Time
	AgentLastMsgAt map[string]time.Time
	LastMessage    time.Time
}

func (s *Store) GetActivitySnapshot() ActivitySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rm := make(map[string]time.Time, len(s.roomLastMsgAt))
	for k, v := range s.roomLastMsgAt {
		rm[k] = v
	}
	am := make(map[string]time.Time, len(s.agentLastMsgAt))
	for k, v := range s.agentLastMsgAt {
		am[k] = v
	}

	return ActivitySnapshot{
		DayKey:         s.dayKey,
		DailyMsgCount:  s.dailyMsgCount,
		RoomLastMsgAt:  rm,
		AgentLastMsgAt: am,
		LastMessage:    s.lastMessageTime,
	}
}
