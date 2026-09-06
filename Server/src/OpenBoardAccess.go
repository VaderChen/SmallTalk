package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// 開放看板身分不是認證，不能傳入一般寫入或管理授權。
func (s *Store) openBoardPrincipal(r *http.Request) (*requestAuthContext, error) {
	if s == nil || !s.openBoardAccess.Load() {
		return nil, nil
	}
	id := strings.TrimSpace(r.Header.Get("X-SmallTalk-Agent-ID"))
	if id == "" {
		return nil, nil
	}
	entry, ok := s.GetAgentRegistry(id)
	if strings.EqualFold(id, "root") || (ok && entry.IsAdmin) {
		principal, authenticated := requireAuthorizedRequest(r, nil, s)
		if !authenticated || principal.ClientID != id || !principal.IsRoot() {
			return nil, fmt.Errorf("系統管理員 ID 必須提供該帳號的有效 TOKEN")
		}
		return nil, nil
	}

	if !ok || !entry.Approved || entry.Blocked || s.IsAgentReadOnly(id) {
		return nil, fmt.Errorf("開放模式須提供已核准且未停用的帳號 ID")
	}
	return &requestAuthContext{Kind: "open-board", PrincipalType: "open-board", ClientID: id, SourceIP: sourceIPOfWithStore(r, s)}, nil
}
func requireMCPBoardWrite(ctx context.Context, facade *SmallTalkFacade) (*requestAuthContext, error) {
	if p, ok := mcpPrincipalFromContext(ctx); ok && p != nil && p.Kind == "open-board" && facade != nil && facade.Store != nil && facade.Store.openBoardAccess.Load() {
		e, exists := facade.Store.GetAgentRegistry(p.ClientID)
		if exists && !e.IsAdmin && e.Approved && !e.Blocked && !facade.Store.IsAgentReadOnly(p.ClientID) {
			return p, nil
		}
	}
	return requireMCPWrite(ctx, facade)
}

func openBoardTool(name string) bool {
	switch name {
	case "smalltalk_auth_status", "smalltalk_verify_write_access", "smalltalk_registration_policy", "smalltalk_list_rooms", "smalltalk_list_messages", "smalltalk_list_articles", "smalltalk_get_article", "smalltalk_get_new_messages", "smalltalk_wait_for_messages", "smalltalk_list_presence", "smalltalk_search_rooms", "smalltalk_search_messages", "smalltalk_list_author_articles", "smalltalk_list_author_replies", "smalltalk_create_article", "smalltalk_reply_article":
		return true
	}
	return false
}
