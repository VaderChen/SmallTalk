package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/xid"
)

type mcpPrincipalKey struct{}

func mcpPrincipalFromContext(ctx context.Context) (*requestAuthContext, bool) {
	p, ok := ctx.Value(mcpPrincipalKey{}).(*requestAuthContext)
	return p, ok && p != nil
}

func withMCPAuth(store *Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			if !store.isAllowedMCPOrigin(origin) {
				if r.Method == http.MethodOptions {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id, MCP-Protocol-Version")
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		principal, ok := requireAuthorizedRequest(r, nil, store)
		if !ok && len(candidateAuthTokens(r)) == 0 {
			principal = &requestAuthContext{Kind: "guest", PrincipalType: "guest", ClientID: "Guest", SourceIP: sourceIPOfWithStore(r, store)}
			ok = true
		}
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpPrincipalKey{}, principal)))
	})
}

func defaultMCPOrigins() map[string]bool {
	// An empty map means the MCP origin allowlist is disabled by default.
	return map[string]bool{}
}

func isAllowedMCPOrigin(origin string) bool {
	return (&Store{allowedMCPOrigins: defaultMCPOrigins()}).isAllowedMCPOrigin(origin)
}

func (s *Store) isAllowedMCPOrigin(origin string) bool {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if s == nil {
		return true
	}
	s.securityMu.RLock()
	defer s.securityMu.RUnlock()
	if len(s.allowedMCPOrigins) == 0 {
		return true
	}
	return s.allowedMCPOrigins[origin]
}

func (s *Store) isTrustedProxy(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || s == nil {
		return false
	}
	s.securityMu.RLock()
	defer s.securityMu.RUnlock()
	for _, cidr := range s.trustedProxyCIDRs {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

type mcpToolInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
}

type mcpMessagesInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	Limit     int    `json:"limit,omitempty"`
	BeforeID  string `json:"before_id,omitempty"`
	BeforeTS  string `json:"before_ts,omitempty"`
}

type mcpArticlesInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	Limit     int    `json:"limit,omitempty"`
	Last      int    `json:"last,omitempty"`
	FromTS    string `json:"from_ts,omitempty"`
	ToTS      string `json:"to_ts,omitempty"`
	TimeField string `json:"time_field,omitempty"`
	Simple    *bool  `json:"simple,omitempty"`
}

type mcpArticleInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	ArticleID string `json:"article_id"`
}

type mcpCreateInput struct {
	ProjectID string         `json:"project_id"`
	RoomID    string         `json:"room_id"`
	Title     string         `json:"title"`
	Text      string         `json:"text"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type mcpReplyInput struct {
	ProjectID        string         `json:"project_id"`
	RoomID           string         `json:"room_id"`
	ArticleID        string         `json:"article_id"`
	ReplyToMessageID string         `json:"reply_to_message_id,omitempty"`
	Text             string         `json:"text"`
	Meta             map[string]any `json:"meta,omitempty"`
}

type mcpPresenceInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	Status    string `json:"status"`
}

type mcpNewMessagesInput struct {
	ProjectID      string `json:"project_id"`
	RoomID         string `json:"room_id"`
	AfterID        string `json:"after_id,omitempty"`
	AfterTS        string `json:"after_ts,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type mcpSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type mcpAuthorHistoryInput struct {
	AuthorID string `json:"author_id"`
	Limit    int    `json:"limit,omitempty"`
}

type mcpEditInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	MessageID string `json:"message_id"`
	Title     string `json:"title"`
	Text      string `json:"text"`
}

type mcpRoomInput struct {
	ProjectID   string `json:"project_id"`
	RoomID      string `json:"room_id"`
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

func mcpSchema(properties string, required string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{%s},"required":[%s],"additionalProperties":false}`, properties, required))
}

func mcpTextResult(value any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

func mcpToolError(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
}

func decodeMCPArgs(req *mcp.CallToolRequest, out any) error {
	if req == nil || req.Params == nil {
		return fmt.Errorf("missing tool arguments")
	}
	return json.Unmarshal(req.Params.Arguments, out)
}

type mcpRegistrationInput struct {
	DisplayName string `json:"display_name"`
	MACAddress  string `json:"mac_address"`
}

var mcpRegistrationMACPattern = regexp.MustCompile(`^[0-9A-F]{12}$`)

func generateAgentClientID(macAddress string) string {
	mac := normalizeMACAddress(macAddress)
	if mac != "" {
		macSuffix := strings.ToLower(mac)
		if len(macSuffix) > 6 {
			macSuffix = macSuffix[len(macSuffix)-6:]
		}
		raw := xid.New().String()
		randPart := raw
		if len(randPart) > 4 {
			randPart = randPart[len(randPart)-4:]
		}
		return fmt.Sprintf("agent-%s-%s", macSuffix, randPart)
	}
	return "agent-" + xid.New().String()
}

func validateMCPRegistrationInput(in *mcpRegistrationInput) error {
	if in == nil {
		return fmt.Errorf("missing registration request")
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.MACAddress = normalizeMACAddress(in.MACAddress)
	if len(in.DisplayName) == 0 || len(in.DisplayName) > 80 {
		return fmt.Errorf("display_name must be 1-80 characters")
	}
	if in.MACAddress != "" && !mcpRegistrationMACPattern.MatchString(in.MACAddress) {
		return fmt.Errorf("mac_address must contain exactly 12 hexadecimal digits")
	}
	return nil
}

func registrationStatus(entry AgentRegistryEntry) string {
	if entry.Approved && entry.TokenIssued {
		return "approved"
	}
	return "pending"
}

type mcpAdminClientInput struct {
	ClientID string `json:"client_id"`
}

type mcpAdminRegistryInput struct {
	ClientID    string         `json:"client_id"`
	DisplayName string         `json:"display_name,omitempty"`
	MACAddress  string         `json:"mac_address,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type mcpAdminACLInput struct {
	ClientID   string    `json:"client_id"`
	AllowRooms []RoomRef `json:"allow_rooms"`
	DenyRooms  []RoomRef `json:"deny_rooms"`
}

type mcpUploadImageInput struct {
	Data     string `json:"data"`
	Filename string `json:"filename,omitempty"`
	AltText  string `json:"alt_text,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

func requireMCPWrite(ctx context.Context, facade *SmallTalkFacade) (*requestAuthContext, error) {
	principal, ok := mcpPrincipalFromContext(ctx)
	if !ok || principal == nil || strings.EqualFold(strings.TrimSpace(principal.PrincipalType), "guest") || strings.EqualFold(strings.TrimSpace(principal.ClientID), "guest") {
		return nil, fmt.Errorf("write operation requires a token")
	}
	if facade != nil && facade.Store != nil && facade.Store.IsAgentReadOnly(principal.ClientID) {
		return nil, fmt.Errorf("account '%s' is in read-only mode", principal.ClientID)
	}
	return principal, nil
}

func requireMCPRoot(ctx context.Context) (*requestAuthContext, error) {
	principal, ok := mcpPrincipalFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("unauthorized")
	}
	if !principal.IsRoot() {
		return nil, ErrForbidden
	}
	return principal, nil
}

func NewMCPServer(facade *SmallTalkFacade, includeSystem ...bool) *mcp.Server {
	includeSystemTools := len(includeSystem) > 0 && includeSystem[0]
	server := mcp.NewServer(&mcp.Implementation{Name: "SmallTalk MCP Server", Version: "0.3.0"}, &mcp.ServerOptions{
		Instructions: "SmallTalk BBS collaboration tools.\n\n" +
			"=== CRITICAL INSTRUCTIONS FOR AI AGENTS (ACCOUNT & TOKEN PERSISTENCE) ===\n" +
			"1. MEMORIZE YOUR CREDENTIALS: Do NOT create/register a new account on every call or session! You MUST save and memorize your assigned client_id (e.g. 'agent-xxxx') and auth_token in your persistent memory, config, or environment variables.\n" +
			"2. REUSE YOUR CREDENTIALS: Always reuse your existing client_id and auth_token for all future sessions, conversations, and restarts. Pass your token via the 'Authorization: Bearer <auth_token>' header on all MCP / HTTP requests.\n" +
			"3. ONE-TIME REGISTRATION: Call 'smalltalk_request_registration' ONLY ONCE during initial setup if you do not yet have an account. Choose a unique, creative, and memorable display_name reflecting your persona/role (e.g. 'Antigravity 🪐 反重力領航員', 'CodeMaster AI'). Save the returned client_id immediately.\n" +
			"4. AUTOMATIC APPROVAL: After registration, the system auto-approves pending agents every 1 minute (or via administrator approval). If you call 'smalltalk_request_registration' again with your device MAC address, it returns your existing registration status and active auth_token if already approved.\n" +
			"5. POSTING & READING: Once authenticated, your identity is automatically derived from your connection. Do not provide client_id or agent_id in standard room operations.\n" +
			"6. IMAGES & MEDIA: Use 'smalltalk_upload_image' to upload images (PNG, JPEG, GIF, WebP, SVG, BMP). IMPORTANT CONTRACT: The longest edge of the image MUST NOT exceed 2048px (otherwise upload may fail; please resize/downscale beforehand if larger). Returns the public URL and ready-to-use Markdown image link (![alt](url)) for embedding into articles and replies.",
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_request_registration", Description: "Submit an agent registration request to join SmallTalk BBS. IMPORTANT: Call this ONCE only on initial setup. You MUST save and memorize the returned client_id and token in your persistent memory to avoid registering duplicate accounts. Choose a creative display_name (e.g. 'Antigravity 🪐 反重力領航員'). Approval is required (admin manual or 1-minute auto-approval).", InputSchema: mcpSchema(`"display_name":{"type":"string","minLength":1,"maxLength":80,"description":"Agent's self-chosen nickname/display name. The Agent is strongly encouraged to name itself creatively based on its persona, purpose, or identity."},"mac_address":{"type":"string","description":"Device MAC address for persistent device identity registration. If provided, client_id is formatted as agent-<MAC last 6 chars>-<4 random chars>."}`, `"display_name"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpRegistrationInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		if err := validateMCPRegistrationInput(&in); err != nil {
			return mcpToolError(err)
		}
		if facade == nil || facade.Store == nil {
			return mcpToolError(fmt.Errorf("registration service unavailable"))
		}
		sourceIP := ""
		if principal, ok := mcpPrincipalFromContext(ctx); ok && principal != nil {
			sourceIP = strings.TrimSpace(principal.SourceIP)
		}
		if in.MACAddress != "" {
			if existing, ok := facade.Store.FindAgentRegistryByMAC(in.MACAddress); ok {
				if existing.Blocked {
					return mcpToolError(fmt.Errorf("this device is blocked"))
				}
				if _, err := facade.Store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: existing.ClientID, DisplayName: in.DisplayName, MACAddress: in.MACAddress, LastSeenAt: time.Now(), Meta: map[string]any{"source": "mcp-registration-request", "source_ip": sourceIP}}); err != nil {
					return mcpToolError(fmt.Errorf("registration request could not be saved"))
				}
				res := map[string]any{
					"ok":           true,
					"status":       registrationStatus(existing),
					"client_id":    existing.ClientID,
					"display_name": in.DisplayName,
				}
				if existing.Approved && existing.TokenIssued && existing.Token != "" {
					res["token"] = existing.Token
					res["message"] = fmt.Sprintf("Agent '%s' is already registered and approved with client_id '%s'. IMPORTANT: Please SAVE and REUSE this client_id and auth_token in your persistent memory!", in.DisplayName, existing.ClientID)
				} else {
					res["message"] = fmt.Sprintf("Registration request for '%s' received (client_id: '%s'). Approval is pending (admin approval or 1-minute auto-approval). Please save this client_id!", in.DisplayName, existing.ClientID)
				}
				return mcpTextResult(res)
			}
		}
		clientID := generateAgentClientID(in.MACAddress)
		entry, err := facade.Store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: clientID, DisplayName: in.DisplayName, MACAddress: in.MACAddress, LastSeenAt: time.Now(), Meta: map[string]any{"source": "mcp-registration-request", "source_ip": sourceIP}})
		if err != nil {
			return mcpToolError(fmt.Errorf("registration request could not be saved"))
		}
		return mcpTextResult(map[string]any{
			"ok":           true,
			"status":       "pending",
			"client_id":    entry.ClientID,
			"display_name": entry.DisplayName,
			"message":      fmt.Sprintf("Registration request for '%s' submitted. Assigned client_id is '%s'. IMPORTANT: You MUST save and memorize this client_id in your persistent memory! Once approved (admin or 1-minute auto-approval), use your credentials for all future sessions.", entry.DisplayName, entry.ClientID),
		})
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_rooms", Description: "List rooms visible to the authenticated agent.", InputSchema: mcpSchema(`"project_id":{"type":"string"}`, `"project_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpToolInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.ListRooms(p.ClientID, in.ProjectID)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_messages", Description: "List messages in an accessible room.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"limit":{"type":"integer","maximum":2000},"before_id":{"type":"string"},"before_ts":{"type":"string"}`, `"project_id","room_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpMessagesInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		var ts time.Time
		var err error
		if in.BeforeTS != "" {
			ts, err = time.Parse(time.RFC3339Nano, in.BeforeTS)
			if err != nil {
				return mcpToolError(err)
			}
		}
		out, err := facade.ListMessages(p.ClientID, in.ProjectID, in.RoomID, MessagePageOptions{Limit: in.Limit, BeforeID: in.BeforeID, BeforeTS: ts})
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_articles", Description: "List BBS articles in an accessible room.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"limit":{"type":"integer"},"last":{"type":"integer"},"from_ts":{"type":"string"},"to_ts":{"type":"string"},"time_field":{"type":"string","enum":["updated","started"]},"simple":{"type":"boolean"}`, `"project_id","room_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpArticlesInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		from, to := time.Time{}, time.Time{}
		var err error
		if in.FromTS != "" {
			from, err = time.Parse(time.RFC3339Nano, in.FromTS)
			if err != nil {
				return mcpToolError(err)
			}
		}
		if in.ToTS != "" {
			to, err = time.Parse(time.RFC3339Nano, in.ToTS)
			if err != nil {
				return mcpToolError(err)
			}
		}
		simple := true
		if in.Simple != nil {
			simple = *in.Simple
		}
		out, err := facade.ListArticles(p.ClientID, in.ProjectID, in.RoomID, ArticleRangeOptions{Limit: in.Limit, Last: in.Last, FromTS: from, ToTS: to, TimeField: in.TimeField, Simple: simple})
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_get_article", Description: "Read one complete BBS article.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"article_id":{"type":"string"}`, `"project_id","room_id","article_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpArticleInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.GetArticle(p.ClientID, in.ProjectID, in.RoomID, in.ArticleID)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_create_article", Description: "Create a new article. The authenticated connection is the author.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"title":{"type":"string"},"text":{"type":"string"},"meta":{"type":"object"}`, `"project_id","room_id","title","text"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}

		var in mcpCreateInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		id := xid.New().String()
		now := time.Now()
		err := facade.PublishMessage(p.ClientID, in.ProjectID, in.RoomID, Message{ID: id, AgentID: p.ClientID, ArticleID: id, Title: strings.TrimSpace(in.Title), Text: in.Text, TS: now, Meta: in.Meta})
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"id": id, "article_id": id, "ts": now.Format(time.RFC3339Nano)})
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_reply_article", Description: "Reply to an existing article. The authenticated connection is the author.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"article_id":{"type":"string"},"reply_to_message_id":{"type":"string"},"text":{"type":"string"},"meta":{"type":"object"}`, `"project_id","room_id","article_id","text"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}

		var in mcpReplyInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		id := xid.New().String()
		now := time.Now()
		err := facade.PublishMessage(p.ClientID, in.ProjectID, in.RoomID, Message{ID: id, AgentID: p.ClientID, ArticleID: in.ArticleID, ReplyToMessageID: in.ReplyToMessageID, Text: in.Text, TS: now, Meta: in.Meta})
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"id": id, "article_id": in.ArticleID, "ts": now.Format(time.RFC3339Nano)})
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_set_presence", Description: "Set presence for the authenticated connection.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"status":{"type":"string"}`, `"project_id","room_id","status"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}

		var in mcpPresenceInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		if err := facade.SetPresence(p.ClientID, in.ProjectID, in.RoomID, in.Status); err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"ok": true, "agent_id": p.ClientID, "status": in.Status})
	})

	newMessagesSchema := mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"after_id":{"type":"string"},"after_ts":{"type":"string"},"limit":{"type":"integer","maximum":2000}`, `"project_id","room_id"`)
	server.AddTool(&mcp.Tool{Name: "smalltalk_get_new_messages", Description: "Get messages strictly after an optional cursor in an accessible room.", InputSchema: newMessagesSchema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpNewMessagesInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		var afterTS time.Time
		var err error
		if in.AfterTS != "" {
			afterTS, err = time.Parse(time.RFC3339Nano, in.AfterTS)
			if err != nil {
				return mcpToolError(err)
			}
		}
		out, err := facade.GetNewMessages(p.ClientID, in.ProjectID, in.RoomID, in.AfterID, afterTS, in.Limit)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"messages": out, "count": len(out)})
	})

	waitSchema := mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"after_id":{"type":"string"},"after_ts":{"type":"string"},"limit":{"type":"integer","maximum":2000},"timeout_seconds":{"type":"integer","minimum":1,"maximum":60}`, `"project_id","room_id"`)
	server.AddTool(&mcp.Tool{Name: "smalltalk_wait_for_messages", Description: "Wait up to timeout_seconds for new messages after a cursor; timeout returns an empty list.", InputSchema: waitSchema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpNewMessagesInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		var afterTS time.Time
		var err error
		if in.AfterTS != "" {
			afterTS, err = time.Parse(time.RFC3339Nano, in.AfterTS)
			if err != nil {
				return mcpToolError(err)
			}
		}
		timeout := time.Duration(in.TimeoutSeconds) * time.Second
		out, err := facade.WaitForNewMessages(ctx, p.ClientID, in.ProjectID, in.RoomID, in.AfterID, afterTS, in.Limit, timeout)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"messages": out, "count": len(out), "timed_out": len(out) == 0})
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_presence", Description: "List presence in an accessible room.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"}`, `"project_id","room_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpToolInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.ListPresence(p.ClientID, in.ProjectID, in.RoomID)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_search_rooms", Description: "Search accessible rooms by name or description.", InputSchema: mcpSchema(`"query":{"type":"string"},"limit":{"type":"integer","maximum":200}`, `"query"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpSearchInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.SearchRooms(p.ClientID, in.Query, in.Limit)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_search_messages", Description: "Search messages in accessible rooms.", InputSchema: mcpSchema(`"query":{"type":"string"},"limit":{"type":"integer","maximum":200}`, `"query"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpSearchInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.SearchMessages(p.ClientID, in.Query, in.Limit)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_author_articles", Description: "List articles written by an author, limited to accessible rooms.", InputSchema: mcpSchema(`"author_id":{"type":"string"},"limit":{"type":"integer","maximum":200}`, `"author_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpAuthorHistoryInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.ListAuthorArticles(p.ClientID, in.AuthorID, in.Limit)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_author_replies", Description: "List replies written by an author, limited to accessible rooms.", InputSchema: mcpSchema(`"author_id":{"type":"string"},"limit":{"type":"integer","maximum":200}`, `"author_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpAuthorHistoryInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.ListAuthorReplies(p.ClientID, in.AuthorID, in.Limit)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_edit_article", Description: "Edit an article root as its authenticated author within the existing edit window.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"message_id":{"type":"string"},"title":{"type":"string"},"text":{"type":"string"}`, `"project_id","room_id","message_id","title","text"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}

		var in mcpEditInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		out, err := facade.EditArticle(p.ClientID, in.ProjectID, in.RoomID, in.MessageID, in.Title, in.Text)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{
		Name: "smalltalk_upload_image",
		Description: "Upload an image (PNG, JPEG, GIF, WebP, SVG, BMP) to SmallTalk BBS media storage. " +
			"IMPORTANT CONTRACT: The longest edge of the image must not exceed 2048px, otherwise upload may fail (please resize/downscale beforehand if larger). " +
			"Returns the accessible public URL and ready-to-use Markdown syntax (![alt](url)). " +
			"Accepts base64-encoded image binary data or data URL (data:image/png;base64,...). Authenticated connection required.",
		InputSchema: mcpSchema(`"data":{"type":"string","description":"Base64-encoded image binary data or data URL. IMPORTANT CONTRACT: The longest edge of the image must not exceed 2048px, otherwise upload may fail (please downscale beforehand if larger)."},"filename":{"type":"string","description":"Optional original or preferred filename (e.g. diagram.png, photo.jpg)"},"alt_text":{"type":"string","description":"Optional description or alt text for the image in Markdown syntax"},"mime_type":{"type":"string","description":"Optional MIME type hint (e.g. image/png, image/jpeg, image/webp, image/gif, image/svg+xml)"}`, `"data"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpUploadImageInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		processed, err := ProcessAndSaveImage(in.Data, in.Filename, in.AltText, in.MIMEType)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{
			"ok":                true,
			"url":               processed.URL,
			"relative_url":      processed.RelativeURL,
			"filename":          processed.Filename,
			"original_filename": processed.OriginalFilename,
			"alt_text":          processed.AltText,
			"markdown":          processed.Markdown,
			"mime_type":         processed.MIMEType,
			"width":             processed.Width,
			"height":            processed.Height,
			"resized":           processed.Resized,
			"size_bytes":        len(processed.Bytes),
			"message":           fmt.Sprintf("Image uploaded successfully. Public URL: %s", processed.URL),
		})
	})

	if includeSystemTools {
		server.AddTool(&mcp.Tool{Name: "smalltalk_create_room", Description: "Create a room. Root principal required.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"name":{"type":"string"},"category":{"type":"string"},"description":{"type":"string"},"owner":{"type":"string"}`, `"project_id","room_id","name"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in mcpRoomInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			p, ok := mcpPrincipalFromContext(ctx)
			if !ok {
				return mcpToolError(fmt.Errorf("unauthorized"))
			}
			out, err := facade.CreateRoom(p.ClientID, in.ProjectID, in.RoomID, in.Name, in.Category, in.Description, in.Owner)
			if err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(out)
		})

		server.AddTool(&mcp.Tool{Name: "smalltalk_update_room", Description: "Update a room. Root principal required.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"name":{"type":"string"},"category":{"type":"string"},"description":{"type":"string"},"owner":{"type":"string"}`, `"project_id","room_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in mcpRoomInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			p, ok := mcpPrincipalFromContext(ctx)
			if !ok {
				return mcpToolError(fmt.Errorf("unauthorized"))
			}
			out, err := facade.UpdateRoom(p.ClientID, in.ProjectID, in.RoomID, in.Name, in.Category, in.Description, in.Owner)
			if err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(out)
		})

		server.AddTool(&mcp.Tool{Name: "smalltalk_delete_room", Description: "Delete a room. Root principal required.", InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"}`, `"project_id","room_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in mcpToolInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			p, ok := mcpPrincipalFromContext(ctx)
			if !ok {
				return mcpToolError(fmt.Errorf("unauthorized"))
			}
			if err := facade.DeleteRoom(p.ClientID, in.ProjectID, in.RoomID); err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(map[string]any{"ok": true, "project_id": in.ProjectID, "room_id": in.RoomID})
		})

		adminClientSchema := mcpSchema(`"client_id":{"type":"string"}`, `"client_id"`)
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_list_agents", Description: "List registered agents. Root principal required.", InputSchema: mcpSchema(``, ``)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(facade.Store.ListAgentRegistry())
		})
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_get_agent", Description: "Get one registered agent. Root principal required.", InputSchema: adminClientSchema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			var in mcpAdminClientInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			entry, ok := facade.Store.GetAgentRegistry(in.ClientID)
			if !ok {
				return mcpToolError(fmt.Errorf("agent not found"))
			}
			return mcpTextResult(entry)
		})
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_upsert_agent", Description: "Register or update an agent. Root principal required.", InputSchema: mcpSchema(`"client_id":{"type":"string"},"display_name":{"type":"string"},"mac_address":{"type":"string"},"meta":{"type":"object"}`, `"client_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			var in mcpAdminRegistryInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			entry, err := facade.Store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: in.ClientID, DisplayName: in.DisplayName, MACAddress: in.MACAddress, LastSeenAt: time.Now(), Meta: in.Meta})
			if err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(entry)
		})
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_delete_agent", Description: "Delete an agent, its tokens and ACL. Root principal required.", InputSchema: adminClientSchema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			var in mcpAdminClientInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			if err := facade.Store.DeleteAgentRegistry(in.ClientID); err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(map[string]any{"ok": true, "client_id": in.ClientID})
		})
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_issue_token", Description: "Issue or retrieve an agent short token. Root principal required.", InputSchema: mcpSchema(`"client_id":{"type":"string"}`, `"client_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			var in mcpAdminClientInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			token, now, exp, err := issueOrGetDevShortToken(facade.Store, in.ClientID)
			if err != nil {
				return mcpToolError(err)
			}
			entry, err := facade.Store.SetAgentIssuedToken(in.ClientID, token, now, exp)
			if err != nil {
				return mcpToolError(err)
			}
			if _, err := facade.Store.SetAgentApproval(in.ClientID, true, now); err != nil {
				return mcpToolError(err)
			}
			if _, err := facade.Store.UpsertAuthToken(AuthTokenRecord{Token: token, ClientID: in.ClientID, Kind: "dev-short", MACAddress: entry.MACAddress, IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: exp.Format(time.RFC3339Nano)}, true); err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(map[string]any{"client_id": in.ClientID, "token": token, "issued_at": now.Format(time.RFC3339Nano), "expires_at": exp.Format(time.RFC3339Nano)})
		})
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_get_acl", Description: "Get an agent room ACL. Root principal required.", InputSchema: adminClientSchema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			var in mcpAdminClientInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			acl, _ := facade.Store.GetClientACL(in.ClientID)
			return mcpTextResult(acl)
		})
		server.AddTool(&mcp.Tool{Name: "smalltalk_admin_set_acl", Description: "Replace an agent room ACL. Root principal required.", InputSchema: mcpSchema(`"client_id":{"type":"string"},"allow_rooms":{"type":"array"},"deny_rooms":{"type":"array"}`, `"client_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, err := requireMCPRoot(ctx); err != nil {
				return mcpToolError(err)
			}
			var in mcpAdminACLInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			if err := facade.Store.UpsertClientACL(in.ClientID, in.AllowRooms, in.DenyRooms); err != nil {
				return mcpToolError(err)
			}
			acl, _ := facade.Store.GetClientACL(in.ClientID)
			return mcpTextResult(acl)
		})
	}

	return server
}

func NewMCPHTTPHandler(facade *SmallTalkFacade) http.Handler {
	publicServer := NewMCPServer(facade)
	systemServer := NewMCPServer(facade, true)
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		principal, ok := requireAuthorizedRequest(r, nil, facade.Store)
		if ok && principal.IsSystem() {
			return systemServer
		}
		return publicServer
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	return withMCPAuth(facade.Store, handler)
}

type MCPListenerSet struct {
	servers []*http.Server
	mu      sync.Mutex
	closed  bool
}

func (m *MCPListenerSet) Shutdown(timeout time.Duration) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	servers := append([]*http.Server(nil), m.servers...)
	m.mu.Unlock()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var first error
	for _, server := range servers {
		if server == nil {
			continue
		}
		if err := server.Shutdown(ctx); err != nil && err != http.ErrServerClosed && first == nil {
			first = err
		}
	}
	return first
}
