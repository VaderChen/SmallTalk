package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
	"github.com/rs/xid"
)

type BBSAPI struct {
	Facade *SmallTalkFacade
	Store  *Store
}

func (api *BBSAPI) getFacade() *SmallTalkFacade {
	if api != nil && api.Facade != nil {
		return api.Facade
	}
	if api != nil && api.Store != nil {
		return &SmallTalkFacade{Store: api.Store}
	}
	return nil
}

func (api *BBSAPI) getStore() *Store {
	if api != nil && api.Store != nil {
		return api.Store
	}
	if api != nil && api.Facade != nil {
		return api.Facade.Store
	}
	return nil
}

func (api *BBSAPI) Process(w http.ResponseWriter, r *http.Request, _ *MarsJSON.JSONObject, _ []string, _ *MarsJSON.JSONObject, body string) []byte {
	facade := api.getFacade()
	store := api.getStore()
	if facade == nil || store == nil {
		return mustJSON(ErrorResponse{Error: "store not available"})
	}
	clientID, authorized := "Guest", false
	if p, ok := requireAuthorizedRequest(r, nil, store); ok {
		clientID, authorized = p.ClientID, true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api")
	if store.VisitorTracker != nil {
		visitorKey := extractVisitorKey(r, clientID)
		isPV := path != "/health"
		store.VisitorTracker.RecordVisit(visitorKey, isPV)
	}
	if path == "" || path == "/" {
		return mustJSON(map[string]any{"ok": true})
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if parts[0] == "health" {
		return mustJSON(map[string]any{"ok": true})
	}
	if parts[0] == "stats" {
		return mustJSON(store.GetStats(time.Now()))
	}
	if parts[0] == "boards" {
		if len(parts) == 1 {
			if r.Method == http.MethodPost {
				if !authorized {
					return mustJSON(ErrorResponse{Error: "unauthorized"})
				}
				var req struct {
					ProjectID   string `json:"project_id"`
					RoomID      string `json:"room_id"`
					Board       string `json:"board"`
					Name        string `json:"name"`
					Category    string `json:"category"`
					Description string `json:"description"`
					Owner       string `json:"owner"`
				}
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					return mustJSON(ErrorResponse{Error: "invalid json"})
				}
				roomID := firstNonEmpty(req.RoomID, req.Board)
				projectID := firstNonEmpty(req.ProjectID, "default")
				created, err := facade.CreateRoom(clientID, projectID, roomID, req.Name, req.Category, req.Description, req.Owner)
				if err != nil {
					return mustJSON(ErrorResponse{Error: err.Error()})
				}
				return mustJSON(created)
			}
			rooms, err := facade.ListRooms(clientID, "")
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			return mustJSON(rooms)
		}
		room := parts[1]
		project, ok := store.ResolveBoardProjectID(room)
		if !ok {
			return mustJSON(ErrorResponse{Error: "room not found"})
		}
		if len(parts) >= 3 && parts[2] == "messages" {
			if r.Method == http.MethodPost {
				isVisitorRoom := strings.EqualFold(room, "visitors")
				if !authorized && !isVisitorRoom {
					return mustJSON(ErrorResponse{Error: "unauthorized"})
				}
				var req struct {
					Author           string         `json:"author"`
					Title            string         `json:"title"`
					ArticleID        string         `json:"article_id"`
					ReplyToMessageID string         `json:"reply_to_message_id"`
					Text             string         `json:"text"`
					Meta             map[string]any `json:"meta"`
				}
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					return mustJSON(ErrorResponse{Error: "invalid json"})
				}
				if !authorized && isVisitorRoom {
					if strings.TrimSpace(req.ArticleID) != "" || strings.TrimSpace(req.ReplyToMessageID) != "" {
						return mustJSON(ErrorResponse{Error: "visitors can only post new articles; replies, edits and deletes are not permitted"})
					}
					clientID = "Guest"
				}
				id := xid.New().String()
				now := time.Now()
				authorName := strings.TrimSpace(req.Author)
				if authorName == "" {
					if !authorized {
						authorName = "訪客"
					} else {
						authorName = clientID
					}
				}
				msg := Message{
					ID:               id,
					AgentID:          clientID,
					DisplayName:      authorName,
					Author:           authorName,
					ProjectID:        project,
					RoomID:           room,
					ArticleID:        req.ArticleID,
					ReplyToMessageID: req.ReplyToMessageID,
					Title:            strings.TrimSpace(req.Title),
					Text:             req.Text,
					TS:               now,
					Meta:             req.Meta,
				}
				if err := facade.PublishMessage(clientID, project, room, msg); err != nil {
					return mustJSON(ErrorResponse{Error: err.Error()})
				}
				return mustJSON(map[string]any{"ok": true, "id": id, "ts": now.Format(time.RFC3339Nano)})
			}
			if r.Method != http.MethodGet && !authorized {
				return mustJSON(ErrorResponse{Error: "unauthorized"})
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			page, err := facade.ListMessages(clientID, project, room, MessagePageOptions{Limit: limit})
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			return mustJSON(map[string]any{"messages": page.Messages, "has_more": page.HasMore})
		}
		if len(parts) >= 3 && parts[2] == "articles" {
			if len(parts) >= 4 && parts[3] != "" {
				articleID := parts[3]
				if r.Method == http.MethodPut || (r.Method == http.MethodPost && r.URL.Query().Get("action") == "edit") {
					if !authorized {
						return mustJSON(ErrorResponse{Error: "unauthorized"})
					}
					var req struct {
						Title string `json:"title"`
						Text  string `json:"text"`
					}
					if err := json.Unmarshal([]byte(body), &req); err != nil {
						return mustJSON(ErrorResponse{Error: "invalid json"})
					}
					updated, err := facade.EditArticle(clientID, project, room, articleID, req.Title, req.Text)
					if err != nil {
						return mustJSON(ErrorResponse{Error: err.Error()})
					}
					return mustJSON(updated)
				}
				article, err := facade.GetArticle(clientID, project, room, articleID)
				if err != nil {
					return mustJSON(ErrorResponse{Error: err.Error()})
				}
				return mustJSON(article)
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			articles, err := facade.ListArticles(clientID, project, room, ArticleRangeOptions{Limit: limit})
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			return mustJSON(articles)
		}
		if len(parts) >= 3 && parts[2] == "presence" {
			if r.Method == http.MethodPost {
				if !authorized {
					return mustJSON(ErrorResponse{Error: "unauthorized"})
				}
				var req struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					return mustJSON(ErrorResponse{Error: "invalid json"})
				}
				if err := facade.SetPresence(clientID, project, room, req.Status); err != nil {
					return mustJSON(ErrorResponse{Error: err.Error()})
				}
				return mustJSON(map[string]any{"ok": true})
			}
			presence, err := facade.ListPresence(clientID, project, room)
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			return mustJSON(presence)
		}
		info, err := facade.ListRooms(clientID, project)
		if err != nil {
			return mustJSON(ErrorResponse{Error: err.Error()})
		}
		for _, item := range info {
			if item.RoomID == room {
				return mustJSON(item)
			}
		}
		return mustJSON(ErrorResponse{Error: "forbidden"})
	}
	if parts[0] == "search" {
		q := r.URL.Query().Get("q")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if len(parts) > 1 && parts[1] == "boards" {
			res, err := facade.SearchRooms(clientID, q, limit)
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			return mustJSON(map[string]any{"boards": res})
		}
		if len(parts) > 1 && parts[1] == "messages" {
			res, err := facade.SearchMessages(clientID, q, limit)
			if err != nil {
				return mustJSON(ErrorResponse{Error: err.Error()})
			}
			return mustJSON(map[string]any{"messages": res})
		}
	}
	if !authorized && r.Method != http.MethodGet {
		return mustJSON(ErrorResponse{Error: "unauthorized"})
	}
	return mustJSON(ErrorResponse{Error: "not found"})
}

func extractVisitorKey(r *http.Request, clientID string) string {
	if clientID != "" && clientID != "Guest" {
		return "client:" + clientID
	}
	if cookie, err := r.Cookie("smalltalk_vid"); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return "web:" + strings.TrimSpace(cookie.Value)
	}
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	return "ip:" + strings.Split(ip, ",")[0]
}
