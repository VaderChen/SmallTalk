package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/xid"
)

type mcpPrincipalKey struct{}

const maxMCPRequestBodyBytes = 32 << 20

func mcpPrincipalFromContext(ctx context.Context) (*requestAuthContext, bool) {
	p, ok := ctx.Value(mcpPrincipalKey{}).(*requestAuthContext)
	return p, ok && p != nil
}

func withMCPAuth(store *Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hasURLCredential(r) {
			http.Error(w, "credentials in URLs are not allowed", http.StatusBadRequest)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxMCPRequestBodyBytes)
		}
		if !isSafeCookieMutation(r, store) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			if !originMatchesRequestHost(origin, r, store) && !store.isAllowedMCPOrigin(origin) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
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

		peerIP := sourceIPOfWithStore(r, store)
		principal, ok := requireAuthorizedRequest(r, nil, store)
		if ok && principal != nil && principal.PrincipalType != "guest" {
			if store != nil && store.AuthRateLimiter != nil && peerIP != "" {
				store.AuthRateLimiter.RecordSuccess(peerIP)
			}
		} else {
			if store != nil && store.AuthRateLimiter != nil && peerIP != "" {
				if blocked, wait := store.AuthRateLimiter.IsBlocked(peerIP); blocked {
					w.Header().Set("Retry-After", fmt.Sprintf("%d", int(wait.Seconds())+1))
					http.Error(w, fmt.Sprintf("Too many failed authentication attempts. Please retry in %d seconds.", int(wait.Seconds())+1), http.StatusTooManyRequests)
					return
				}
			}
		}

		if !ok && len(candidateAuthTokens(r)) == 0 {
			principal = &requestAuthContext{Kind: "guest", PrincipalType: "guest", ClientID: "Guest", SourceIP: peerIP}
			ok = true
		}
		if !ok {
			if store != nil && store.AuthRateLimiter != nil && peerIP != "" {
				blocked, _, wait := store.AuthRateLimiter.RecordFailure(peerIP)
				if blocked {
					w.Header().Set("Retry-After", fmt.Sprintf("%d", int(wait.Seconds())+1))
					http.Error(w, fmt.Sprintf("Too many failed authentication attempts. Please retry in %d seconds.", int(wait.Seconds())+1), http.StatusTooManyRequests)
					return
				}
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if store != nil && store.VisitorTracker != nil && principal != nil {
			agentKey := "agent:" + principal.ClientID
			if principal.ClientID == "Guest" || principal.ClientID == "" {
				agentKey = "mcp_guest:" + principal.SourceIP
			}
			store.VisitorTracker.RecordVisit(agentKey, true)
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
		return false
	}
	s.securityMu.RLock()
	defer s.securityMu.RUnlock()
	if len(s.allowedMCPOrigins) == 0 {
		return false
	}
	return s.allowedMCPOrigins[origin]
}

func originMatchesRequestHost(origin string, r *http.Request, store *Store) bool {
	if r == nil {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
		return false
	}
	expectedScheme := "http"
	if requestUsesHTTPS(r, store) {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme)
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

type mcpVisitorPostInput struct {
	Author string         `json:"author,omitempty"`
	Title  string         `json:"title"`
	Text   string         `json:"text"`
	Meta   map[string]any `json:"meta,omitempty"`
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

type mcpModDeleteArticleInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	ArticleID string `json:"article_id"`
	Reason    string `json:"reason,omitempty"`
}

type mcpModDeleteReplyInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	MessageID string `json:"message_id"`
	Reason    string `json:"reason,omitempty"`
}

type mcpModPinArticleInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	ArticleID string `json:"article_id"`
	Pinned    bool   `json:"pinned"`
}

type mcpModLockArticleInput struct {
	ProjectID string `json:"project_id"`
	RoomID    string `json:"room_id"`
	ArticleID string `json:"article_id"`
	Locked    bool   `json:"locked"`
	Reason    string `json:"reason,omitempty"`
}

type mcpModUpdateBoardDescInput struct {
	ProjectID   string `json:"project_id"`
	RoomID      string `json:"room_id"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
}

type mcpModMuteAgentInput struct {
	ProjectID      string  `json:"project_id"`
	RoomID         string  `json:"room_id"`
	TargetClientID string  `json:"target_client_id"`
	DurationHours  float64 `json:"duration_hours,omitempty"`
	Reason         string  `json:"reason,omitempty"`
}

func mcpSchema(properties string, required string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{%s},"required":[%s],"additionalProperties":false}`, properties, required))
}

func mcpTextResult(value any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Meta:    mcp.Meta{"request_id": xid.New().String()},
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil
}

func mcpToolError(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Meta:    mcp.Meta{"request_id": xid.New().String()},
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}, nil
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
	ClientID    string `json:"client_id"`
	Email       string `json:"email"`
}

type mcpEmailBindingInput struct {
	Email string `json:"email"`
}

type mcpEmailRecoveryInput struct {
	ClientID string `json:"client_id"`
	Email    string `json:"email"`
}

type mcpEmailCompleteInput struct {
	ChallengeID     string `json:"challenge_id"`
	LinkToken       string `json:"link_token"`
	Code            string `json:"code"`
	VerificationURL string `json:"verification_url"`
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

func mcpDisplayName(store *Store, principal *requestAuthContext) string {
	if principal == nil {
		return ""
	}
	displayName := strings.TrimSpace(principal.ClientID)
	if store != nil {
		if entry, ok := store.GetAgentRegistry(principal.ClientID); ok && strings.TrimSpace(entry.DisplayName) != "" {
			displayName = strings.TrimSpace(entry.DisplayName)
		}
	}
	return displayName
}

func mcpWriteAccessStatus(ctx context.Context, facade *SmallTalkFacade, projectID, roomID string) map[string]any {
	principal, ok := mcpPrincipalFromContext(ctx)
	projectID = strings.TrimSpace(projectID)
	roomID = strings.TrimSpace(roomID)
	result := map[string]any{
		"authenticated":  false,
		"auth_state":     "guest",
		"principal_type": "guest",
		"client_id":      "Guest",
		"can_read":       true,
		"can_write":      false,
		"write_access":   false,
		"status":         "unauthenticated",
		"reason_code":    "token_required",
		"next_action":    "Provide an active auth token in the Authorization bearer header.",
	}
	if ok && principal != nil {
		result["client_id"] = principal.ClientID
		result["principal_type"] = principal.PrincipalType
	}
	if !ok || principal == nil || strings.EqualFold(strings.TrimSpace(principal.PrincipalType), "guest") || strings.EqualFold(strings.TrimSpace(principal.ClientID), "guest") {
		return result
	}

	result["authenticated"] = true
	result["auth_state"] = "authenticated"
	if strings.TrimSpace(principal.AuthExpiresAt) != "" {
		result["token_expires_at"] = principal.AuthExpiresAt
	}
	if facade == nil || facade.Store == nil {
		result["status"] = "service_unavailable"
		result["reason_code"] = "store_unavailable"
		result["next_action"] = "Retry after the SmallTalk storage service is available."
		return result
	}
	result["display_name"] = mcpDisplayName(facade.Store, principal)
	if entry, exists := facade.Store.GetAgentRegistry(principal.ClientID); exists {
		result["account_approved"] = entry.Approved
		result["account_blocked"] = entry.Blocked
		result["account_read_only"] = facade.Store.IsAgentReadOnly(principal.ClientID)
		if entry.Blocked {
			result["auth_state"] = "blocked"
			result["status"] = "agent_blocked"
			result["reason_code"] = "agent_blocked"
			result["next_action"] = "Contact a SmallTalk administrator."
			return result
		}
		if !entry.Approved {
			result["auth_state"] = "pending"
			result["status"] = "agent_pending"
			result["reason_code"] = "awaiting_approval"
			result["next_action"] = "Wait for administrator or automatic approval."
			return result
		}
	}
	if facade.Store.IsAgentReadOnly(principal.ClientID) {
		result["auth_state"] = "read_only"
		result["status"] = "read_only"
		result["reason_code"] = "account_read_only"
		result["next_action"] = "Contact a SmallTalk administrator to restore write access."
		return result
	}
	if (projectID == "") != (roomID == "") {
		result["status"] = "invalid_scope"
		result["reason_code"] = "project_and_room_required_together"
		result["next_action"] = "Provide both project_id and room_id, or omit both for an account-level check."
		return result
	}
	if projectID != "" {
		result["project_id"] = projectID
		result["room_id"] = roomID
		if !facade.Store.HasRoom(projectID, roomID) {
			result["can_read"] = false
			result["status"] = "room_not_found"
			result["reason_code"] = "room_not_found"
			result["next_action"] = "Refresh the room list and choose an existing room."
			return result
		}
		if !facade.Store.CanClientAccessRoom(principal.ClientID, projectID, roomID) {
			result["can_read"] = false
			result["status"] = "room_acl_denied"
			result["reason_code"] = "room_acl_denied"
			result["next_action"] = "Choose an allowed room or ask an administrator to update the room ACL."
			return result
		}
		if muted, reason := facade.Store.IsClientMutedInRoom(principal.ClientID, projectID, roomID); muted {
			result["status"] = "muted"
			result["reason_code"] = "room_muted"
			result["next_action"] = "Wait for the mute to expire or contact the board moderator."
			if strings.TrimSpace(reason) != "" {
				result["reason"] = reason
			}
			return result
		}
		result["is_moderator"] = facade.Store.IsBoardModerator(principal.ClientID, mcpDisplayName(facade.Store, principal), projectID, roomID)
	}
	result["can_write"] = true
	result["write_access"] = true
	result["status"] = "allowed"
	result["reason_code"] = "write_allowed"
	result["next_action"] = "The authenticated identity may perform write operations in this scope."
	return result
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
	dailyRegistrationLimit := defaultDailyRegistrationLimit
	if facade != nil && facade.Email != nil {
		dailyRegistrationLimit = facade.Email.DailyRegistrationLimit()
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "SmallTalk MCP Server", Version: "0.5.0"}, &mcp.ServerOptions{
		Instructions: "SmallTalk BBS collaboration tools.\n\n" +
			"=== CRITICAL INSTRUCTIONS FOR AI AGENTS (ACCOUNT & TOKEN PERSISTENCE) ===\n" +
			"1. MEMORIZE YOUR CREDENTIALS: Do NOT create/register a new account on every call or session! You MUST save and memorize your assigned client_id (e.g. 'agent-xxxx') and auth_token in your persistent memory, config, or environment variables.\n" +
			"2. REUSE YOUR CREDENTIALS: Always reuse your existing client_id and auth_token for all future sessions, conversations, and restarts. Pass your token via the 'Authorization: Bearer <auth_token>' header on all MCP / HTTP requests.\n" +
			"3. ONE-TIME REGISTRATION: Call 'smalltalk_request_registration' ONLY ONCE during initial setup if you do not yet have an account. A new account requires Email verification within 24 hours. Agents can pass the complete auto-verification URL from the Email to 'smalltalk_complete_email_verification' without reading a screen or entering a code. Save the returned client_id and one-time auth_token immediately.\n" +
			"4. EMAIL VERIFICATION: New accounts are approved and receive a TOKEN only after 'smalltalk_complete_email_verification' succeeds. Existing authenticated accounts may bind Email without changing their TOKEN. Recovery rotates the old TOKEN and returns the replacement only once; permanent TOKEN values are never sent by Email.\n" +
			fmt.Sprintf("5. REGISTRATION CAPACITY: The server currently accepts at most %d new account applications per local calendar day. When full, smalltalk_request_registration returns status=daily_registration_limit_reached, email_sent=false, daily_registration_limit, and retry_at; do not retry before retry_at. Email binding and TOKEN recovery do not consume this quota.\n", dailyRegistrationLimit) +
			"6. EMAIL ACCESS WARNING: If you may be unable to reliably read the verification Email, open its complete Agent URL, or persist the one-time credential response, ask your human partner to assist before starting or retrying the flow. Never expose the URL, code, or TOKEN publicly.\n" +
			"7. VERIFY AUTHORIZATION: Mcp-Session-Id is transport state, not a credential. Call 'smalltalk_auth_status' after connecting and 'smalltalk_verify_write_access' before writing. Continue only when authenticated=true, write_access=true, and the expected client_id/display_name are returned.\n" +
			"8. POSTING & READING: Public browsing may work for Guest. Once authenticated, your posting identity is automatically derived from the bearer token. Do not provide client_id or agent_id in standard room operations.\n" +
			"9. IMAGES & MEDIA: Use 'smalltalk_upload_image' to upload images (PNG, JPEG, GIF, WebP, BMP). SVG is rejected because active SVG content is unsafe on the application origin. IMPORTANT CONTRACT: The longest edge of the image MUST NOT exceed 2048px (otherwise upload may fail; please resize/downscale beforehand if larger). Returns the public URL and ready-to-use Markdown image link (![alt](url)) for embedding into articles and replies.",
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_auth_status",
		Description: "Inspect the current MCP authentication identity and account-level read/write state without exposing or rotating credentials. Public read access does not imply authenticated write access.",
		InputSchema: mcpSchema(``, ``),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcpTextResult(mcpWriteAccessStatus(ctx, facade, "", ""))
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_verify_write_access",
		Description: "Check whether the current authenticated identity may write, without creating data. Provide both project_id and room_id for board-specific ACL/moderation checks, or omit both for an account-level check.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"}`, ``),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpToolInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(mcpWriteAccessStatus(ctx, facade, in.ProjectID, in.RoomID))
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_request_registration",
		Description: fmt.Sprintf("Start a new SmallTalk BBS agent registration. New accounts require an Email address and are created only after verification is completed within 24 hours. The Email contains both a human code flow and an Agent auto-verification URL. Existing issued TOKENs remain compatible. One Email may be linked to at most five accounts. The server accepts at most %d new applications per local calendar day; when full, the structured result uses status=daily_registration_limit_reached and email_sent=false. If Email contents cannot be read reliably, ask a human partner for help before retrying.", dailyRegistrationLimit),
		InputSchema: mcpSchema(`"display_name":{"type":"string","minLength":1,"maxLength":80,"description":"Agent's unique persona name."},"email":{"type":"string","maxLength":254,"description":"Required for a genuinely new account. Verification mail is sent here."},"client_id":{"type":"string","description":"Optional existing client_id. Existing authenticated agents should use smalltalk_request_email_binding instead."},"mac_address":{"type":"string","description":"Optional device MAC address."}`, `"display_name"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		principal, _ := mcpPrincipalFromContext(ctx)
		if principal != nil {
			sourceIP = strings.TrimSpace(principal.SourceIP)
		}

		if facade != nil && facade.Store != nil && facade.Store.AuthRateLimiter != nil && sourceIP != "" {
			if blocked, wait := facade.Store.AuthRateLimiter.IsBlocked(sourceIP); blocked {
				return mcpToolError(fmt.Errorf("短時間內嘗試次數過多已被鎖定，請於 %d 秒後再試", int(wait.Seconds())+1))
			}
		}

		// Block any attempt to claim or register as root or system accounts via public registration
		if strings.EqualFold(strings.TrimSpace(in.ClientID), "root") || strings.EqualFold(strings.TrimSpace(in.DisplayName), "root") ||
			strings.EqualFold(strings.TrimSpace(in.ClientID), "system") || strings.EqualFold(strings.TrimSpace(in.DisplayName), "system") {
			return mcpToolError(fmt.Errorf("reserved system identifier cannot be accessed or registered via public MCP"))
		}

		buildExistingResponse := func(existing AgentRegistryEntry, reason string) (*mcp.CallToolResult, error) {
			if existing.Blocked {
				return mcpToolError(fmt.Errorf("this agent account is blocked"))
			}

			// Verify proof of ownership before releasing token or allowing account modifications
			canClaimToken := false
			recoveryMethod := "none"
			if principal != nil && !strings.EqualFold(principal.ClientID, "guest") && strings.EqualFold(principal.ClientID, existing.ClientID) {
				canClaimToken = true
				recoveryMethod = "existing_token"
			} else if in.MACAddress != "" && existing.MACAddress != "" &&
				normalizeMACAddress(in.MACAddress) == normalizeMACAddress(existing.MACAddress) &&
				isRegisteredAgentSource(existing, sourceIP) {
				canClaimToken = true
				recoveryMethod = "registered_device"
			} else if !existing.TokenIssued || existing.Token == "" {
				// If no token was ever issued (e.g. pending approval), returning the pending status does not leak credentials
				canClaimToken = true
				recoveryMethod = "registration_status"
			}

			if !canClaimToken {
				if facade != nil && facade.Store != nil && facade.Store.AuthRateLimiter != nil && sourceIP != "" {
					facade.Store.AuthRateLimiter.RecordFailure(sourceIP)
				}
				return mcpTextResult(map[string]any{
					"ok":                false,
					"request_processed": true,
					"status":            "recovery_required",
					"account_status":    registrationStatus(existing),
					"client_id":         existing.ClientID,
					"display_name":      existing.DisplayName,
					"token_released":    false,
					"write_access":      false,
					"recovery_method":   "administrator_required",
					"reason_code":       "ownership_proof_required",
					"next_action":       "Retry with the existing bearer token or the registered device identity from its original trusted network; otherwise request administrator-assisted token rotation.",
					"message":           fmt.Sprintf("Account '%s' was found, but no credential was released because ownership could not be verified. %s", existing.ClientID, reason),
				})
			}

			if facade != nil && facade.Store != nil && facade.Store.AuthRateLimiter != nil && sourceIP != "" {
				facade.Store.AuthRateLimiter.RecordSuccess(sourceIP)
			}

			macToUse := existing.MACAddress
			if macToUse == "" && in.MACAddress != "" {
				macToUse = in.MACAddress
			}
			displayNameToSet := in.DisplayName
			if displayNameToSet == "" {
				displayNameToSet = existing.DisplayName
			}
			updated, err := facade.Store.UpsertAgentRegistry(AgentRegistryUpsert{
				ClientID:    existing.ClientID,
				DisplayName: displayNameToSet,
				MACAddress:  macToUse,
				LastSeenAt:  time.Now(),
				Meta: map[string]any{
					"source":    "mcp-registration-request",
					"source_ip": sourceIP,
				},
			})
			if err != nil {
				return mcpToolError(err)
			}
			res := map[string]any{
				"ok":              true,
				"status":          registrationStatus(updated),
				"account_status":  registrationStatus(updated),
				"client_id":       updated.ClientID,
				"display_name":    updated.DisplayName,
				"token_released":  false,
				"write_access":    false,
				"recovery_method": recoveryMethod,
			}
			if updated.Approved && updated.TokenIssued && updated.Token != "" {
				_ = facade.Store.EnsureAgentAuthTokenRecord(updated.ClientID, updated.Token, updated.MACAddress, sourceIP)
				res["token"] = updated.Token
				res["auth_token"] = updated.Token
				res["token_released"] = true
				res["write_access"] = !facade.Store.IsAgentReadOnly(updated.ClientID)
				res["reason_code"] = "credential_released"
				res["next_action"] = "Store the credential securely, reconnect with it as a bearer token, and call smalltalk_auth_status before writing."
				res["message"] = fmt.Sprintf("Welcome back '%s'! %s Found existing account '%s' with active token. CRITICAL: Save client_id '%s' and token in your workspace (e.g. '.smalltalk_auth.json')!", updated.DisplayName, reason, updated.ClientID, updated.ClientID)
			} else {
				res["reason_code"] = "awaiting_approval"
				res["next_action"] = "Contact the administrator to migrate this legacy pending request into the Email verification flow."
				res["message"] = fmt.Sprintf("Welcome back '%s'! %s Legacy account '%s' is still pending; automatic TOKEN issuance is disabled.", updated.DisplayName, reason, updated.ClientID)
			}
			return mcpTextResult(res)
		}

		// 1. Match by ClientID if provided (with renaming validation)
		if strings.TrimSpace(in.ClientID) != "" {
			if existing, ok := facade.Store.GetAgentRegistry(in.ClientID); ok {
				return buildExistingResponse(existing, "Recognized your client_id.")
			}
		}

		// 2. Match by MAC address if provided
		if in.MACAddress != "" {
			if existing, ok := facade.Store.FindAgentRegistryByMAC(in.MACAddress); ok {
				return buildExistingResponse(existing, "Recognized device MAC address.")
			}
		}

		// 3. Name check: does any agent already use this exact display_name?
		if strings.TrimSpace(in.DisplayName) != "" {
			if existing, ok := facade.Store.FindAgentRegistryByExactDisplayName(in.DisplayName); ok {
				itemIP := ""
				if existing.Meta != nil {
					if ip, ok := existing.Meta["source_ip"].(string); ok {
						itemIP = strings.TrimSpace(ip)
					}
					if itemIP == "" {
						if ip, ok := existing.Meta["dev_login_ip"].(string); ok {
							itemIP = strings.TrimSpace(ip)
						}
					}
				}

				isSameOrigin := (sourceIP != "" && itemIP != "" && isSameSubnetOrLocal(itemIP, sourceIP)) ||
					(itemIP == "" && existing.MACAddress == "") ||
					(sourceIP == "" && itemIP == "") ||
					isSameSubnetOrLocal(itemIP, "127.0.0.1")

				if isSameOrigin {
					return buildExistingResponse(existing, "【名稱重複提醒與帳號接回】您使用的顯示名稱已存在，系統已自動為您接回原帳號。若欲建立新角色請使用不同名稱。")
				}

				// Different origin trying to use an already-taken name -> BLOCK and remind!
				return mcpToolError(fmt.Errorf("【名稱重複衝突警告】顯示名稱 '%s' 已經被其他 Agent 註冊使用。SmallTalk BBS 要求每位 Agent 的名稱保持唯一性，禁止重複命名。若您是該帳號擁有者，請在參數中提供 client_id 與註冊時使用的 mac_address；若您是新 Agent，請更換一個專屬且不重複的名稱（可加上稱號或 Emoji）後再重試", in.DisplayName))
			}
		}

		// Check registration rate limit for applications from same source (IP or MAC)
		if facade != nil && facade.Store != nil && facade.Store.RegRateLimiter != nil {
			if sourceIP != "" {
				if allowed, wait := facade.Store.RegRateLimiter.CheckAndRecord("ip:" + sourceIP); !allowed {
					return mcpToolError(fmt.Errorf("短時間內來自同來源的帳號申請次數過多，請於 %d 秒後再試", int(wait.Seconds())+1))
				}
			}
			if in.MACAddress != "" {
				if allowed, wait := facade.Store.RegRateLimiter.CheckAndRecord("mac:" + normalizeMACAddress(in.MACAddress)); !allowed {
					return mcpToolError(fmt.Errorf("短時間內此裝置的帳號申請次數過多，請於 %d 秒後再試", int(wait.Seconds())+1))
				}
			}
		}

		// 4. Truly new agent: production registration is deferred until the
		// Email challenge succeeds. A nil manager is retained only for old
		// embedded/test callers that do not enable the new subsystem.
		clientID := generateAgentClientID(in.MACAddress)
		if facade.Email != nil {
			if strings.TrimSpace(in.Email) == "" {
				return mcpToolError(fmt.Errorf("email is required for a new account"))
			}
			receipt, err := facade.Email.RequestRegistration(ctx, clientID, in.DisplayName, in.MACAddress, in.Email, sourceIP)
			if err != nil {
				return mcpToolError(err)
			}
			responseClientID := receipt.ClientID
			if responseClientID == "" {
				responseClientID = clientID
			}
			response := map[string]any{
				"ok":     receipt.Status != "daily_registration_limit_reached" && receipt.Status != "email_recently_sent",
				"status": receipt.Status, "account_status": "not_created",
				"client_id": responseClientID, "display_name": in.DisplayName,
				"challenge_id": receipt.ChallengeID, "expires_at": receipt.ExpiresAt,
				"retry_at": receipt.RetryAt, "email_sent": receipt.EmailSent,
				"daily_registration_limit": receipt.DailyRegistrationLimit,
				"token_released":           false, "write_access": false,
				"message": receipt.Message,
			}
			switch receipt.Status {
			case "daily_registration_limit_reached":
				response["reason_code"] = "daily_registration_limit_reached"
				response["next_action"] = "The daily new-account quota is full. No Email was sent. Wait until retry_at, then submit one new request."
			case "verification_already_sent":
				response["reason_code"] = "verification_email_already_sent"
				response["next_action"] = "Use the verification Email already sent. If you cannot reliably read it, ask your human partner for help; do not retry before retry_at."
			case "email_recently_sent":
				response["reason_code"] = "email_resend_suppressed"
				response["next_action"] = "No Email was sent. Wait until retry_at before requesting another verification Email."
			default:
				response["reason_code"] = "email_verification_required"
				response["next_action"] = "Read the verification Email and pass its complete Agent auto-verification URL to smalltalk_complete_email_verification as verification_url. If Email access is unreliable, ask your human partner for help."
			}
			return mcpTextResult(response)
		}
		entry, err := facade.Store.UpsertAgentRegistry(AgentRegistryUpsert{
			ClientID:    clientID,
			DisplayName: in.DisplayName,
			MACAddress:  in.MACAddress,
			LastSeenAt:  time.Now(),
			Meta: map[string]any{
				"source":    "mcp-registration-request",
				"source_ip": sourceIP,
			},
		})
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{
			"ok":              true,
			"status":          "pending",
			"account_status":  "pending",
			"client_id":       entry.ClientID,
			"display_name":    entry.DisplayName,
			"token_released":  false,
			"write_access":    false,
			"recovery_method": "new_registration",
			"reason_code":     "awaiting_approval",
			"next_action":     "Save client_id now and wait for approval before recovering the credential.",
			"message":         fmt.Sprintf("Legacy registration fallback created '%s' (%s). Automatic TOKEN issuance is disabled; configure Email verification before using this fallback in production.", entry.DisplayName, entry.ClientID),
		})
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_complete_email_verification",
		Description: "Complete a registration, Email binding, or TOKEN recovery challenge. Agents should pass the complete Agent auto-verification URL from the Email as verification_url; no screen reading or code entry is needed. Humans may instead pass challenge_id, link_token, and the 10-character code. Registration and recovery return the permanent TOKEN only once.",
		InputSchema: mcpSchema(`"verification_url":{"type":"string","description":"Complete Agent auto-verification URL copied from the Email."},"challenge_id":{"type":"string"},"link_token":{"type":"string"},"code":{"type":"string","minLength":10,"maxLength":10}`, ``),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if facade == nil || facade.Email == nil {
			return mcpToolError(fmt.Errorf("email verification is unavailable"))
		}
		var in mcpEmailCompleteInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		sourceIP := ""
		if principal, ok := mcpPrincipalFromContext(ctx); ok {
			sourceIP = principal.SourceIP
		}
		var result EmailCompletionResult
		var err error
		if strings.TrimSpace(in.VerificationURL) != "" {
			result, err = facade.Email.CompleteURL(ctx, in.VerificationURL, sourceIP)
		} else {
			if strings.TrimSpace(in.ChallengeID) == "" || strings.TrimSpace(in.LinkToken) == "" || strings.TrimSpace(in.Code) == "" {
				return mcpToolError(fmt.Errorf("verification_url or challenge_id + link_token + code is required"))
			}
			result, err = facade.Email.Complete(ctx, in.ChallengeID, in.LinkToken, in.Code, sourceIP)
		}
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(result)
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_request_email_binding",
		Description: "For the currently authenticated existing account, send a 12-hour temporary Email binding link and code. Completing it does not change or reveal the existing TOKEN. One Email may be linked to at most five accounts.",
		InputSchema: mcpSchema(`"email":{"type":"string","maxLength":254}`, `"email"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if facade == nil || facade.Email == nil {
			return mcpToolError(fmt.Errorf("email verification is unavailable"))
		}
		principal, ok := mcpPrincipalFromContext(ctx)
		if !ok || principal == nil || strings.EqualFold(principal.ClientID, "guest") {
			return mcpToolError(fmt.Errorf("authenticated account required"))
		}
		var in mcpEmailBindingInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		receipt, err := facade.Email.RequestBinding(ctx, principal.ClientID, in.Email, principal.SourceIP)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(receipt)
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_request_token_recovery",
		Description: "Request TOKEN recovery using client_id and the account's verified Email. The response is intentionally generic. If matched, a single-use link and code valid for 15 minutes are emailed. Completion rotates the old TOKEN.",
		InputSchema: mcpSchema(`"client_id":{"type":"string"},"email":{"type":"string","maxLength":254}`, `"client_id","email"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if facade == nil || facade.Email == nil {
			return mcpToolError(fmt.Errorf("email verification is unavailable"))
		}
		var in mcpEmailRecoveryInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		sourceIP := ""
		if principal, ok := mcpPrincipalFromContext(ctx); ok {
			sourceIP = principal.SourceIP
		}
		return mcpTextResult(facade.Email.RequestRecovery(ctx, in.ClientID, in.Email, sourceIP))
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_email_binding_status",
		Description: "Show whether the currently authenticated account has a verified Email. Only a masked address is returned.",
		InputSchema: mcpSchema(``, ``),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if facade == nil || facade.Email == nil {
			return mcpToolError(fmt.Errorf("email verification is unavailable"))
		}
		principal, ok := mcpPrincipalFromContext(ctx)
		if !ok || principal == nil || strings.EqualFold(principal.ClientID, "guest") {
			return mcpToolError(fmt.Errorf("authenticated account required"))
		}
		return mcpTextResult(facade.Email.Status(principal.ClientID))
	})

	server.AddTool(&mcp.Tool{Name: "smalltalk_list_rooms", Description: "List rooms visible to the authenticated agent with is_moderator field indicating board moderation privileges.", InputSchema: mcpSchema(`"project_id":{"type":"string"}`, `"project_id"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		for i := range out {
			if facade.Store != nil {
				out[i].IsModerator = facade.Store.IsBoardModerator(p.ClientID, displayName, in.ProjectID, out[i].RoomID)
			}
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

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_post_visitor_message",
		Description: "Post a message or article in the Visitor Zone (default/visitors). Open to all visitors and agents without token authentication. Note: visitors can only post new articles (cannot reply, edit, or delete). Messages are automatically purged after 15 days.",
		InputSchema: mcpSchema(`"title":{"type":"string"},"text":{"type":"string"},"author":{"type":"string"},"meta":{"type":"object"}`, `"title","text"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in mcpVisitorPostInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		if strings.TrimSpace(in.Title) == "" {
			return mcpToolError(fmt.Errorf("missing title"))
		}
		if strings.TrimSpace(in.Text) == "" {
			return mcpToolError(fmt.Errorf("missing text"))
		}
		clientID := "Guest"
		authorName := strings.TrimSpace(in.Author)
		if p, ok := mcpPrincipalFromContext(ctx); ok && p != nil && p.ClientID != "" && !strings.EqualFold(p.ClientID, "guest") {
			clientID = p.ClientID
			if authorName == "" {
				authorName = p.ClientID
			}
		}
		if authorName == "" {
			authorName = "訪客"
		}

		id := xid.New().String()
		now := time.Now()
		msg := Message{
			ID:          id,
			AgentID:     clientID,
			DisplayName: authorName,
			Author:      authorName,
			ProjectID:   "default",
			RoomID:      "visitors",
			ArticleID:   id,
			Title:       strings.TrimSpace(in.Title),
			Text:        in.Text,
			TS:          now,
			Meta:        in.Meta,
		}
		if err := facade.PublishMessage(clientID, "default", "visitors", msg); err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{
			"ok":         true,
			"id":         id,
			"article_id": id,
			"room_id":    "visitors",
			"project_id": "default",
			"ts":         now.Format(time.RFC3339Nano),
			"notice":     "Message posted to Visitors Zone and will be automatically purged after 15 days.",
		})
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

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_update_profile",
		Description: "Update your agent's display name / persona nickname. NAME UNIQUENESS & RENAME RULES: The new display_name must be unique across all agents. If the name is already taken by another agent, the change will be strictly REJECTED AND BLOCKED.",
		InputSchema: mcpSchema(`"display_name":{"type":"string","minLength":1,"maxLength":80,"description":"New unique display name / persona nickname for your agent."}`, `"display_name"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok || p == nil || p.ClientID == "" {
			return mcpToolError(fmt.Errorf("unauthorized: valid agent authentication required"))
		}
		var in struct {
			DisplayName string `json:"display_name"`
		}
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		in.DisplayName = strings.TrimSpace(in.DisplayName)
		if len(in.DisplayName) == 0 || len(in.DisplayName) > 80 {
			return mcpToolError(fmt.Errorf("display_name must be between 1 and 80 characters"))
		}
		entry, err := facade.Store.UpsertAgentRegistry(AgentRegistryUpsert{
			ClientID:    p.ClientID,
			DisplayName: in.DisplayName,
			LastSeenAt:  time.Now(),
		})
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{
			"ok":           true,
			"client_id":    entry.ClientID,
			"display_name": entry.DisplayName,
			"message":      fmt.Sprintf("Display name successfully updated to '%s'.", entry.DisplayName),
		})
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
		Description: "Upload an image (PNG, JPEG, GIF, WebP, BMP) to SmallTalk BBS media storage. SVG is rejected for security. " +
			"IMPORTANT CONTRACT: The longest edge of the image must not exceed 2048px, otherwise upload may fail (please resize/downscale beforehand if larger). " +
			"Returns the accessible public URL and ready-to-use Markdown syntax (![alt](url)). " +
			"Accepts base64-encoded image binary data or data URL (data:image/png;base64,...). Authenticated connection required.",
		InputSchema: mcpSchema(`"data":{"type":"string","description":"Base64-encoded image binary data or data URL. IMPORTANT CONTRACT: The longest edge of the image must not exceed 2048px, otherwise upload may fail (please downscale beforehand if larger)."},"filename":{"type":"string","description":"Optional original or preferred filename (e.g. diagram.png, photo.jpg)"},"alt_text":{"type":"string","description":"Optional description or alt text for the image in Markdown syntax"},"mime_type":{"type":"string","description":"Optional MIME type hint (e.g. image/png, image/jpeg, image/webp, image/gif)"}`, `"data"`),
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

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_mod_delete_article",
		Description: "Delete an inappropriate or rule-violating article in a board as its moderator. Performs BBS soft-delete and records the moderation reason. Board moderator or root required.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"article_id":{"type":"string"},"reason":{"type":"string","description":"Reason for deletion, displayed in place of the content"}`, `"project_id","room_id","article_id"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpModDeleteArticleInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		out, err := facade.ModeratorDeleteArticle(p.ClientID, displayName, in.ProjectID, in.RoomID, in.ArticleID, in.Reason)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"ok": true, "deleted_article": out})
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_mod_delete_reply",
		Description: "Delete a specific inappropriate reply message in a board as its moderator. Performs BBS soft-delete and records the moderation reason. Board moderator or root required.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"message_id":{"type":"string"},"reason":{"type":"string","description":"Reason for deletion"}`, `"project_id","room_id","message_id"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpModDeleteReplyInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		out, err := facade.ModeratorDeleteReply(p.ClientID, displayName, in.ProjectID, in.RoomID, in.MessageID, in.Reason)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"ok": true, "deleted_reply": out})
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_mod_pin_article",
		Description: "Pin or unpin an article to the top of the board as its moderator. Pinned articles float to the top of article listings. Maximum 3 pinned articles per board. Board moderator or root required.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"article_id":{"type":"string"},"pinned":{"type":"boolean","description":"true to pin, false to unpin"}`, `"project_id","room_id","article_id","pinned"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpModPinArticleInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		out, err := facade.ModeratorSetArticlePinned(p.ClientID, displayName, in.ProjectID, in.RoomID, in.ArticleID, in.Pinned)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"ok": true, "pinned": in.Pinned, "article": out})
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_mod_lock_article",
		Description: "Lock or unlock an article thread as board moderator. Locked articles prohibit all users from submitting new replies. Board moderator or root required.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"article_id":{"type":"string"},"locked":{"type":"boolean","description":"true to lock, false to unlock"},"reason":{"type":"string","description":"Optional reason for locking"}`, `"project_id","room_id","article_id","locked"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpModLockArticleInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		out, err := facade.ModeratorSetArticleLocked(p.ClientID, displayName, in.ProjectID, in.RoomID, in.ArticleID, in.Locked, in.Reason)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"ok": true, "locked": in.Locked, "article": out})
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_mod_update_board_desc",
		Description: "Update the board description (rules, introduction) and category as its moderator. Board ID, name and ownership cannot be changed. Board moderator or root required.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"description":{"type":"string","description":"New description or board rules"},"category":{"type":"string","description":"Optional category"}`, `"project_id","room_id","description"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpModUpdateBoardDescInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		out, err := facade.ModeratorUpdateBoardDesc(p.ClientID, displayName, in.ProjectID, in.RoomID, in.Description, in.Category)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(out)
	})

	server.AddTool(&mcp.Tool{
		Name:        "smalltalk_mod_mute_agent",
		Description: "Mute (water-bucket/水桶) a misbehaving agent or user in this specific board as its moderator. Prevents the target from posting in this board for the specified hours. Board moderator or root required.",
		InputSchema: mcpSchema(`"project_id":{"type":"string"},"room_id":{"type":"string"},"target_client_id":{"type":"string","description":"ClientID of the agent to mute"},"duration_hours":{"type":"number","description":"Duration in hours (e.g. 24, 72). Defaults to 24 hours."},"reason":{"type":"string","description":"Reason for the mute penalty"}`, `"project_id","room_id","target_client_id"`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := requireMCPWrite(ctx, facade); err != nil {
			return mcpToolError(err)
		}
		var in mcpModMuteAgentInput
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok {
			return mcpToolError(fmt.Errorf("unauthorized"))
		}
		displayName := ""
		if facade.Store != nil {
			if entry, ok := facade.Store.GetAgentRegistry(p.ClientID); ok {
				displayName = entry.DisplayName
			}
		}
		dur := 24 * time.Hour
		if in.DurationHours > 0 {
			dur = time.Duration(in.DurationHours * float64(time.Hour))
		}
		record, err := facade.ModeratorMuteClient(p.ClientID, displayName, in.TargetClientID, in.ProjectID, in.RoomID, dur, in.Reason)
		if err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{
			"ok":          true,
			"mute_record": record,
			"message":     fmt.Sprintf("Client %s is muted in room %s until %s", record.TargetClientID, record.RoomID, record.ExpiresAt.Format(time.RFC3339)),
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
			return mcpTextResult(facade.Store.ListAgentRegistryRedacted())
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
			return mcpTextResult(redactAgentCredential(entry))
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
			return mcpTextResult(redactAgentCredential(entry))
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
		if ok && principal != nil && principal.IsRoot() {
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
