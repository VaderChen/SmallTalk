package main

import "time"

// SnapshotRooms returns snapshots of all rooms and their in-memory messages.
// This is used by the periodic exporter.
func (s *Store) SnapshotRooms() []RoomSnapshot {
	s.mu.RLock()
	type roomRef struct {
		pid string
		rid string
		r   *Room
	}
	var rooms []roomRef
	for pid, p := range s.projects {
		for rid, r := range p.Rooms {
			rooms = append(rooms, roomRef{pid: pid, rid: rid, r: r})
		}
	}
	s.mu.RUnlock()

	out := make([]RoomSnapshot, 0, len(rooms))
	now := time.Now()
	for _, item := range rooms {
		item.r.mu.RLock()
		msgs := make([]Message, len(item.r.Messages))
		copy(msgs, item.r.Messages)
		board := firstNonEmpty(item.r.Board, item.rid)
		item.r.mu.RUnlock()

		out = append(out, RoomSnapshot{
			ProjectID:  item.pid,
			RoomID:     item.rid,
			Board:      board,
			ExportedAt: now,
			Messages:   msgs,
		})
	}
	return out
}
