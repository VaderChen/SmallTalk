package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ClientRoomACL struct {
	Allow map[string]bool
	Deny  map[string]bool
}

type RoomRef struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
}

type ClientACLResponse struct {
	ClientID   string    `json:"client_id"`
	AllowRooms []RoomRef `json:"allow_rooms"`
	DenyRooms  []RoomRef `json:"deny_rooms"`
}

type UpdateClientACLRequest struct {
	ClientID   string    `json:"client_id,omitempty"`
	AllowRooms []RoomRef `json:"allow_rooms"`
	DenyRooms  []RoomRef `json:"deny_rooms"`
}

type aclDiskEntry struct {
	AllowRooms []RoomRef `json:"allow_rooms"`
	DenyRooms  []RoomRef `json:"deny_rooms"`
}

func (s *Store) aclPath() string {
	return filepath.Join(s.dataDir, "client_room_acls.json")
}

func roomAccessKey(projectID, roomID string) string {
	return strings.TrimSpace(projectID) + "/" + strings.TrimSpace(roomID)
}

func parseRoomAccessKey(key string) RoomRef {
	parts := strings.SplitN(strings.TrimSpace(key), "/", 2)
	if len(parts) != 2 {
		return RoomRef{}
	}
	return RoomRef{ProjectID: parts[0], RoomID: parts[1]}
}

func normalizeRoomRefs(in []RoomRef) []RoomRef {
	seen := map[string]bool{}
	out := make([]RoomRef, 0, len(in))
	for _, rr := range in {
		rr.ProjectID = strings.TrimSpace(rr.ProjectID)
		rr.RoomID = strings.TrimSpace(rr.RoomID)
		if rr.ProjectID == "" || rr.RoomID == "" {
			continue
		}
		key := roomAccessKey(rr.ProjectID, rr.RoomID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].RoomID < out[j].RoomID
	})
	return out
}

func roomRefsToSet(in []RoomRef) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, rr := range normalizeRoomRefs(in) {
		out[roomAccessKey(rr.ProjectID, rr.RoomID)] = true
	}
	return out
}

func roomSetToRefs(in map[string]bool) []RoomRef {
	out := make([]RoomRef, 0, len(in))
	for key, ok := range in {
		if !ok {
			continue
		}
		rr := parseRoomAccessKey(key)
		if rr.ProjectID == "" || rr.RoomID == "" {
			continue
		}
		out = append(out, rr)
	}
	return normalizeRoomRefs(out)
}

func (s *Store) LoadACLs() error {
	path := s.aclPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var disk map[string]aclDiskEntry
	if err := json.Unmarshal(b, &disk); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomACLs = make(map[string]*ClientRoomACL, len(disk))
	for clientID, entry := range disk {
		clientID = strings.TrimSpace(clientID)
		if clientID == "" {
			continue
		}
		s.roomACLs[clientID] = &ClientRoomACL{
			Allow: roomRefsToSet(entry.AllowRooms),
			Deny:  roomRefsToSet(entry.DenyRooms),
		}
	}
	return nil
}

func (s *Store) SaveACLs() error {
	s.mu.RLock()
	disk := make(map[string]aclDiskEntry, len(s.roomACLs))
	for clientID, acl := range s.roomACLs {
		if acl == nil {
			continue
		}
		disk[clientID] = aclDiskEntry{
			AllowRooms: roomSetToRefs(acl.Allow),
			DenyRooms:  roomSetToRefs(acl.Deny),
		}
	}
	s.mu.RUnlock()

	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	if s.pg != nil {
		s.mu.RLock()
		for cid, acl := range s.roomACLs {
			if acl != nil {
				_ = s.pg.SaveRoomACL(cid, acl.Allow, acl.Deny)
			}
		}
		s.mu.RUnlock()
	}
	if s.dataDir == "" {
		return nil
	}
	return os.WriteFile(s.aclPath(), b, 0644)
}

func (s *Store) UpsertClientACL(clientID string, allowRooms, denyRooms []RoomRef) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ErrMissingClientID
	}

	s.mu.Lock()
	s.roomACLs[clientID] = &ClientRoomACL{
		Allow: roomRefsToSet(allowRooms),
		Deny:  roomRefsToSet(denyRooms),
	}
	s.mu.Unlock()
	return s.SaveACLs()
}

func (s *Store) GetClientACL(clientID string) (ClientACLResponse, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ClientACLResponse{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	acl, ok := s.roomACLs[clientID]
	if !ok || acl == nil {
		return ClientACLResponse{ClientID: clientID}, false
	}
	return ClientACLResponse{
		ClientID:   clientID,
		AllowRooms: roomSetToRefs(acl.Allow),
		DenyRooms:  roomSetToRefs(acl.Deny),
	}, true
}

func (s *Store) CanClientAccessRoom(clientID, projectID, roomID string) bool {
	clientID = strings.TrimSpace(clientID)
	projectID = strings.TrimSpace(projectID)
	roomID = strings.TrimSpace(roomID)
	if clientID == "" || projectID == "" || roomID == "" {
		return false
	}

	key := roomAccessKey(projectID, roomID)

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canClientAccessRoomLocked(clientID, key)
}

func (s *Store) canClientAccessRoomLocked(clientID, key string) bool {
	acl, ok := s.roomACLs[clientID]
	if !ok || acl == nil {
		return true
	}

	if acl.Allow[key] {
		return true
	}
	if acl.Deny[key] {
		return false
	}
	if len(acl.Allow) == 0 && len(acl.Deny) == 0 {
		return true
	}
	if len(acl.Allow) > 0 {
		return false
	}
	return true
}

func (s *Store) ListRoomsForClient(projectID, clientID string) ([]Room, error) {
	rooms, err := s.ListRooms(projectID)
	if err != nil {
		return nil, err
	}
	out := make([]Room, 0, len(rooms))
	for _, room := range rooms {
		if s.CanClientAccessRoom(clientID, projectID, room.ID) {
			out = append(out, room)
		}
	}
	return out, nil
}

func (s *Store) ListAllRoomsForClient(clientID string, now time.Time) []RoomInfo {
	rooms := s.ListAllRooms(now)
	out := make([]RoomInfo, 0, len(rooms))
	for _, room := range rooms {
		if s.CanClientAccessRoom(clientID, room.ProjectID, room.RoomID) {
			out = append(out, room)
		}
	}
	return out
}
