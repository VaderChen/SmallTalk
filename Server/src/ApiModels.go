package main

import "time"

type AuthLoginRequest struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	Project  string `json:"project,omitempty"`
}

type AuthLoginResponse struct {
	OK        bool   `json:"ok"`
	Account   string `json:"account"`
	Project   string `json:"project,omitempty"`
	AuthToken string `json:"auth_token,omitempty"`
}

type AuthProjectOption struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type AuthProjectsResponse struct {
	Projects []AuthProjectOption `json:"projects"`
}

type AuthIssueTokenRequest struct {
	TTLSec  int `json:"ttl_sec,omitempty"`
	TTLDays int `json:"ttl_days,omitempty"`
}

type DevRegisterRequest struct {
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name,omitempty"`
	MACAddress  string `json:"mac_address"`
}

type DevRegisterResponse struct {
	OK          bool   `json:"ok"`
	Status      string `json:"status,omitempty"`
	Reason      string `json:"reason,omitempty"`
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name,omitempty"`
	MACAddress  string `json:"mac_address,omitempty"`
	Approved    bool   `json:"approved"`
	Blocked     bool   `json:"blocked"`
	TokenIssued bool   `json:"token_issued"`
	Project     string `json:"project,omitempty"`
	AuthToken   string `json:"auth_token,omitempty"`
	Message     string `json:"message,omitempty"`
}

type DevLoginRequest struct {
	ClientID   string `json:"client_id"`
	MACAddress string `json:"mac_address"`
}

type DevLoginResponse struct {
	OK          bool   `json:"ok"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name,omitempty"`
	MACAddress  string `json:"mac_address,omitempty"`
	Approved    bool   `json:"approved"`
	Blocked     bool   `json:"blocked"`
	TokenIssued bool   `json:"token_issued"`
	Project     string `json:"project,omitempty"`
	AuthToken   string `json:"auth_token,omitempty"`
	Message     string `json:"message,omitempty"`
}

func nowRFC3339() string { return time.Now().Format(time.RFC3339Nano) }

type MessagePageOptions struct {
	Limit    int
	BeforeID string
	BeforeTS time.Time
}

type MessagePage struct {
	Messages     []Message `json:"messages"`
	HasMore      bool      `json:"has_more"`
	NextBeforeID string    `json:"next_before_id,omitempty"`
	NextBeforeTS string    `json:"next_before_ts,omitempty"`
}

type ArticleRangeOptions struct {
	Limit     int
	Last      int
	FromTS    time.Time
	ToTS      time.Time
	TimeField string
	Simple    bool
}

type ArticleSummary struct {
	ProjectID     string    `json:"project_id"`
	RoomID        string    `json:"room_id"`
	Board         string    `json:"board"`
	ArticleID     string    `json:"article_id"`
	Article       string    `json:"article"`
	Title         string    `json:"title,omitempty"`
	Author        string    `json:"author"`
	RootMessageID string    `json:"root_message_id"`
	RootMessage   string    `json:"message"`
	StartedTS     string    `json:"started_ts"`
	UpdatedTS     string    `json:"updated_ts"`
	ReplyCount    int       `json:"reply_count"`
	Body          string    `json:"body,omitempty"`
	Replies       []Message `json:"replies,omitempty"`
}

type SearchRoomsResponse struct {
	Query string     `json:"query"`
	Rooms []RoomInfo `json:"rooms"`
}

type MessageSearchHit struct {
	ProjectID string  `json:"project_id"`
	RoomID    string  `json:"room_id"`
	Board     string  `json:"board"`
	RoomName  string  `json:"room_name,omitempty"`
	BoardName string  `json:"board_name,omitempty"`
	Message   Message `json:"message"`
}

type SearchMessagesResponse struct {
	Query    string             `json:"query"`
	Messages []MessageSearchHit `json:"messages"`
}
