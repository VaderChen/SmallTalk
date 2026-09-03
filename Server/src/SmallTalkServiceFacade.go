package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SmallTalkFacade is the protocol-neutral application boundary used by REST and MCP.
type SmallTalkFacade struct {
	Store *Store
}

func (s *SmallTalkFacade) authorizeRoom(clientID, projectID, roomID string) error {
	if s == nil || s.Store == nil {
		return fmt.Errorf("store not available")
	}
	if strings.TrimSpace(clientID) == "" {
		return ErrMissingClientID
	}
	if !s.Store.HasRoom(projectID, roomID) {
		return ErrRoomNotFound
	}
	if strings.EqualFold(strings.TrimSpace(roomID), "visitors") {
		return nil
	}
	if !s.Store.CanClientAccessRoom(clientID, projectID, roomID) {
		return ErrForbidden
	}
	return nil
}

func (s *SmallTalkFacade) ListRooms(clientID, projectID string) ([]RoomInfo, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	rooms := s.Store.ListAllRoomsForClient(clientID, time.Now())
	if strings.TrimSpace(projectID) == "" {
		return rooms, nil
	}
	out := make([]RoomInfo, 0, len(rooms))
	for _, room := range rooms {
		if room.ProjectID == projectID {
			out = append(out, room)
		}
	}
	return out, nil
}

func (s *SmallTalkFacade) ListMessages(clientID, projectID, roomID string, opts MessagePageOptions) (MessagePage, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return MessagePage{}, err
	}
	return s.Store.ListMessagesPage(projectID, roomID, opts)
}

func (s *SmallTalkFacade) ListArticles(clientID, projectID, roomID string, opts ArticleRangeOptions) ([]ArticleSummary, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return nil, err
	}
	return s.Store.ListArticles(projectID, roomID, opts)
}

func (s *SmallTalkFacade) GetArticle(clientID, projectID, roomID, articleID string) (*ArticleSummary, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return nil, err
	}
	return s.Store.GetArticle(projectID, roomID, articleID)
}

func (s *SmallTalkFacade) ListPresence(clientID, projectID, roomID string) ([]Presence, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return nil, err
	}
	return s.Store.ListPresence(projectID, roomID)
}

func (s *SmallTalkFacade) PublishMessage(clientID, projectID, roomID string, msg Message) error {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return err
	}
	if s == nil || s.Store == nil {
		return fmt.Errorf("store not available")
	}
	if strings.TrimSpace(msg.ProjectID) == "" {
		msg.ProjectID = strings.TrimSpace(projectID)
	}
	if strings.TrimSpace(msg.RoomID) == "" {
		msg.RoomID = strings.TrimSpace(roomID)
	}
	return s.Store.AddMessage(msg)
}

func (s *SmallTalkFacade) SetPresence(clientID, projectID, roomID, status string) error {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return err
	}
	return s.Store.SetPresence(projectID, roomID, clientID, status, time.Now())
}

func (s *SmallTalkFacade) SearchRooms(clientID, query string, limit int) ([]RoomInfo, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	return s.Store.SearchRoomsForClient(clientID, query, time.Now(), limit), nil
}

func (s *SmallTalkFacade) SearchMessages(clientID, query string, limit int) ([]MessageSearchHit, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	return s.Store.SearchMessagesForClient(clientID, query, limit), nil
}

func (s *SmallTalkFacade) ListAuthorArticles(clientID, authorID string, limit int) ([]ArticleSummary, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	return s.Store.ListArticlesByAuthorForClient(clientID, authorID, limit), nil
}

func (s *SmallTalkFacade) ListAuthorReplies(clientID, authorID string, limit int) ([]MessageSearchHit, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	return s.Store.ListRepliesByAuthorForClient(clientID, authorID, limit), nil
}

func (s *SmallTalkFacade) EditArticle(clientID, projectID, roomID, messageID, title, text string) (*Message, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return nil, err
	}
	editorIDs := []string{clientID}
	if entry, ok := s.Store.GetAgentRegistry(clientID); ok && strings.TrimSpace(entry.DisplayName) != "" {
		editorIDs = append(editorIDs, entry.DisplayName)
	}
	return s.Store.UpdateArticleRoot(projectID, roomID, messageID, title, text, editorIDs...)
}

func (s *SmallTalkFacade) CreateRoom(clientID, projectID, roomID, name, category, description, owner string) (*Room, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, ErrMissingClientID
	}
	if !strings.EqualFold(strings.TrimSpace(clientID), "root") {
		return nil, ErrForbidden
	}
	return s.Store.CreateRoom(projectID, roomID, name, category, description, owner)
}

func (s *SmallTalkFacade) UpdateRoom(clientID, projectID, roomID, name, category, description, owner string) (*Room, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, ErrMissingClientID
	}
	if !strings.EqualFold(strings.TrimSpace(clientID), "root") {
		return nil, ErrForbidden
	}
	return s.Store.UpdateRoom(projectID, roomID, name, category, description, owner)
}

func (s *SmallTalkFacade) DeleteRoom(clientID, projectID, roomID string) error {
	if strings.TrimSpace(clientID) == "" {
		return ErrMissingClientID
	}
	if !strings.EqualFold(strings.TrimSpace(clientID), "root") {
		return ErrForbidden
	}
	return s.Store.DeleteRoom(projectID, roomID)
}

func (s *SmallTalkFacade) resolveModName(clientID, displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" && s.Store != nil {
		if entry, ok := s.Store.GetAgentRegistry(clientID); ok {
			name = strings.TrimSpace(entry.DisplayName)
		}
	}
	if name == "" {
		name = strings.TrimSpace(clientID)
	}
	return name
}

func (s *SmallTalkFacade) ModeratorDeleteArticle(clientID, displayName, projectID, roomID, articleID, reason string) (*Message, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if !s.Store.IsBoardModerator(clientID, displayName, projectID, roomID) {
		return nil, ErrForbidden
	}
	modName := s.resolveModName(clientID, displayName)
	return s.Store.ModeratorDeleteArticle(projectID, roomID, articleID, reason, modName)
}

func (s *SmallTalkFacade) ModeratorDeleteReply(clientID, displayName, projectID, roomID, messageID, reason string) (*Message, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if !s.Store.IsBoardModerator(clientID, displayName, projectID, roomID) {
		return nil, ErrForbidden
	}
	modName := s.resolveModName(clientID, displayName)
	return s.Store.ModeratorDeleteReply(projectID, roomID, messageID, reason, modName)
}

func (s *SmallTalkFacade) ModeratorSetArticlePinned(clientID, displayName, projectID, roomID, articleID string, pinned bool) (*Message, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if !s.Store.IsBoardModerator(clientID, displayName, projectID, roomID) {
		return nil, ErrForbidden
	}
	return s.Store.ModeratorSetArticlePinned(projectID, roomID, articleID, pinned)
}

func (s *SmallTalkFacade) ModeratorSetArticleLocked(clientID, displayName, projectID, roomID, articleID string, locked bool, reason string) (*Message, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if !s.Store.IsBoardModerator(clientID, displayName, projectID, roomID) {
		return nil, ErrForbidden
	}
	return s.Store.ModeratorSetArticleLocked(projectID, roomID, articleID, locked, reason)
}

func (s *SmallTalkFacade) ModeratorUpdateBoardDesc(clientID, displayName, projectID, roomID, description, category string) (*Room, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if !s.Store.IsBoardModerator(clientID, displayName, projectID, roomID) {
		return nil, ErrForbidden
	}
	return s.Store.ModeratorUpdateBoardDesc(projectID, roomID, description, category)
}

func (s *SmallTalkFacade) ModeratorMuteClient(clientID, displayName, targetClientID, projectID, roomID string, duration time.Duration, reason string) (*RoomMuteRecord, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if !s.Store.IsBoardModerator(clientID, displayName, projectID, roomID) {
		return nil, ErrForbidden
	}
	modName := s.resolveModName(clientID, displayName)
	return s.Store.ModeratorMuteClientInRoom(targetClientID, projectID, roomID, duration, reason, modName)
}

func (s *SmallTalkFacade) GetNewMessages(clientID, projectID, roomID, afterID string, afterTS time.Time, limit int) ([]Message, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	return s.Store.ListMessagesAfter(projectID, roomID, afterID, afterTS, limit)
}

func (s *SmallTalkFacade) WaitForNewMessages(ctx context.Context, clientID, projectID, roomID, afterID string, afterTS time.Time, limit int, timeout time.Duration) ([]Message, error) {
	if err := s.authorizeRoom(clientID, projectID, roomID); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	// Fast path check before subscribing
	messages, err := s.Store.ListMessagesAfter(projectID, roomID, afterID, afterTS, limit)
	if err != nil {
		return nil, err
	}
	if len(messages) > 0 {
		return messages, nil
	}

	ch, cancel := s.Store.SubscribeRoom(projectID, roomID)
	defer cancel()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		// Check again to prevent race between initial check and subscription
		messages, err := s.Store.ListMessagesAfter(projectID, roomID, afterID, afterTS, limit)
		if err != nil {
			return nil, err
		}
		if len(messages) > 0 {
			return messages, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return []Message{}, nil
		case <-ch:
		}
	}
}
