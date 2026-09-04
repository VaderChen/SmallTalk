package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPSecurity_NoTokenLeakWithoutProofOfPossession(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	facade := &SmallTalkFacade{Store: store}
	server := NewMCPServer(facade)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(context.Background(), serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	ctx := context.Background()

	// 1. Legitimate admin agent registers and receives approval & token
	adminEntry, err := store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    "agent-admin-001",
		DisplayName: "峨嵋派Hermes",
		MACAddress:  "AABBCCDDEEFF",
		LastSeenAt:  time.Now(),
		Meta: map[string]any{
			"source":       "manual-setup",
			"source_ip":    "198.51.100.25",
			"dev_login_ip": "198.51.100.25",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mark as approved admin with an active token
	adminToken := "secret-admin-token-12345"
	adminEntry.Approved = true
	adminEntry.TokenIssued = true
	adminEntry.IsAdmin = true
	adminEntry.Token = adminToken
	store.mu.Lock()
	store.agentRegistry[adminEntry.ClientID] = &adminEntry
	store.mu.Unlock()
	_ = store.SaveRegistry()

	// 2. Attacker attempts to register with same display name: must NOT leak client_id!
	conflictResult, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "峨嵋派Hermes",
		},
	})
	if conflictResult == nil || !conflictResult.IsError {
		t.Fatalf("expected conflict error, got: %#v", conflictResult)
	}
	conflictText := conflictResult.Content[0].(*mcp.TextContent).Text
	if strings.Contains(conflictText, "agent-admin-001") {
		t.Fatalf("CRITICAL: conflict message leaked victim client_id: %s", conflictText)
	}

	// 3. Attacker discovers victim's client_id from a post and tries to claim token via smalltalk_request_registration:
	// Attacker provides NO MAC or WRONG MAC -> Token MUST NOT be returned!
	claimResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "Attacker",
			"client_id":    "agent-admin-001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var claimResp map[string]any
	if err := json.Unmarshal([]byte(claimResult.Content[0].(*mcp.TextContent).Text), &claimResp); err != nil {
		t.Fatalf("failed to unmarshal claim result: %v, text: %s", err, claimResult.Content[0].(*mcp.TextContent).Text)
	}
	if tok, hasToken := claimResp["token"]; hasToken && tok != "" && tok != nil {
		t.Fatalf("CRITICAL SECURITY VULNERABILITY: Unauthenticated caller was given victim's token: %v", tok)
	}

	// 4. Attacker provides wrong MAC address -> Token MUST NOT be returned!
	wrongMACResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "Attacker",
			"client_id":    "agent-admin-001",
			"mac_address":  "11:22:33:44:55:66",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wrongMACResp map[string]any
	if err := json.Unmarshal([]byte(wrongMACResult.Content[0].(*mcp.TextContent).Text), &wrongMACResp); err != nil {
		t.Fatal(err)
	}
	if tok, hasToken := wrongMACResp["token"]; hasToken && tok != "" && tok != nil {
		t.Fatalf("CRITICAL: Attacker with wrong MAC received token: %v", tok)
	}

	// 5. Legitimate owner provides correct MAC address -> Token IS successfully returned!
	legitResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "峨嵋派Hermes",
			"client_id":    "agent-admin-001",
			"mac_address":  "AA:BB:CC:DD:EE:FF",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var legitResp map[string]any
	if err := json.Unmarshal([]byte(legitResult.Content[0].(*mcp.TextContent).Text), &legitResp); err != nil {
		t.Fatal(err)
	}
	if legitResp["token"] != adminToken {
		t.Fatalf("expected legitimate owner with correct MAC to receive token %q, got %#v", adminToken, legitResp)
	}

	// 6. Attacker attempts to claim or register 'root' or 'system' -> MUST BE REJECTED!
	rootResult, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "root",
			"client_id":    "root",
		},
	})
	if rootResult == nil || !rootResult.IsError {
		t.Fatalf("expected attempt to claim root to be rejected, got: %#v", rootResult)
	}
}

func TestMCPSecurity_SVGScriptInjectionBlocked(t *testing.T) {
	// Test SVG with executable script is rejected
	svgPayload := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.cookie)</script></svg>`))
	_, err := ProcessAndSaveImage(svgPayload, "test.svg", "test", "image/svg+xml")
	if err == nil {
		t.Fatalf("expected SVG with <script> to be rejected, but succeeded")
	}

	// Test SVG with onload event handler is rejected
	svgPayloadOnload := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="fetch('https://evil.com')"><circle cx="10" cy="10" r="5"/></svg>`))
	_, err = ProcessAndSaveImage(svgPayloadOnload, "test.svg", "test", "image/svg+xml")
	if err == nil {
		t.Fatalf("expected SVG with onload to be rejected, but succeeded")
	}

	// Clean valid SVG should be allowed
	validSVG := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="50" cy="50" r="40" fill="red"/></svg>`))
	processed, err := ProcessAndSaveImage(validSVG, "valid.svg", "valid", "image/svg+xml")
	if err != nil {
		t.Fatalf("expected clean SVG to succeed, got: %v", err)
	}
	if processed == nil || !strings.HasSuffix(processed.Filename, ".svg") {
		t.Fatalf("unexpected processed SVG: %#v", processed)
	}
}

func TestMCPSecurity_TokenRetryRateLimiting(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	store.AuthRateLimiter = NewAuthRateLimiter(3, 1*time.Minute, 5*time.Minute)
	facade := &SmallTalkFacade{Store: store}
	server := NewMCPServer(facade)

	// Create an existing approved account with token
	target, err := store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    "agent-victim-001",
		DisplayName: "TargetAgent",
		MACAddress:  "001122334455",
		LastSeenAt:  time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	target.Approved = true
	target.TokenIssued = true
	target.Token = "secret-token-victim"
	store.mu.Lock()
	store.agentRegistry[target.ClientID] = &target
	store.mu.Unlock()

	attackerIP := "198.51.100.77"
	serverCtx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{
		ClientID:      "Guest",
		PrincipalType: "guest",
		SourceIP:      attackerIP,
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "attacker", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(serverCtx, serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	ctx := context.Background()

	// Attempt 1: Wrong MAC
	res1, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "TargetAgent",
			"client_id":    "agent-victim-001",
			"mac_address":  "DEADBEEF0001",
		},
	})
	var out1 map[string]any
	_ = json.Unmarshal([]byte(res1.Content[0].(*mcp.TextContent).Text), &out1)
	if _, ok := out1["token"]; ok {
		t.Fatalf("unexpected token returned on attempt 1")
	}

	// Attempt 2: Wrong MAC
	_, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "TargetAgent",
			"client_id":    "agent-victim-001",
			"mac_address":  "DEADBEEF0002",
		},
	})

	// Attempt 3: Wrong MAC -> hits threshold (3 failures)
	_, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "TargetAgent",
			"client_id":    "agent-victim-001",
			"mac_address":  "DEADBEEF0003",
		},
	})

	// Attempt 4: Should be locked out!
	res4, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "TargetAgent",
			"client_id":    "agent-victim-001",
			"mac_address":  "001122334455", // Even with the right MAC now, should be blocked!
		},
	})
	if res4 == nil || !res4.IsError {
		t.Fatalf("expected attempt 4 to be blocked by rate limiter lockout, got: %#v", res4)
	}
	errText := res4.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(errText, "已被鎖定") && !strings.Contains(errText, "短時間內嘗試次數過多") {
		t.Fatalf("expected lockout message, got: %s", errText)
	}

	// Reset attacker IP and verify unlock
	store.AuthRateLimiter.Reset(attackerIP)
	resLegit, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "TargetAgent",
			"client_id":    "agent-victim-001",
			"mac_address":  "001122334455",
		},
	})
	if resLegit == nil || resLegit.IsError {
		t.Fatalf("expected success after reset, got error: %#v", resLegit)
	}
}

func TestMCPSecurity_RegistrationRateLimitingSameSource(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	// Allow at most 2 new registrations in 5 minutes, then 10 minute lockout
	store.RegRateLimiter = NewAuthRateLimiter(2, 5*time.Minute, 10*time.Minute)
	facade := &SmallTalkFacade{Store: store}
	server := NewMCPServer(facade)

	sameIP := "203.0.113.42"
	serverCtx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{
		ClientID:      "Guest",
		PrincipalType: "guest",
		SourceIP:      sameIP,
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "reg-tester", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(serverCtx, serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	ctx := context.Background()

	// 1st registration from same IP -> allowed
	res1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "NewBotAlpha",
			"mac_address":  "AA:BB:CC:11:11:01",
		},
	})
	if err != nil || (res1 != nil && res1.IsError) {
		t.Fatalf("registration 1 failed: %v, %#v", err, res1)
	}

	// 2nd registration from same IP -> allowed
	res2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "NewBotBeta",
			"mac_address":  "AA:BB:CC:11:11:02",
		},
	})
	if err != nil || (res2 != nil && res2.IsError) {
		t.Fatalf("registration 2 failed: %v, %#v", err, res2)
	}

	// 3rd registration from same IP -> EXCEEDS limit (max 2), must be blocked!
	res3, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "NewBotGamma",
			"mac_address":  "AA:BB:CC:11:11:03",
		},
	})
	if res3 == nil || !res3.IsError {
		t.Fatalf("expected 3rd registration from same source IP to be blocked, got: %#v", res3)
	}
	errText := res3.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(errText, "短時間內來自同來源的帳號申請次數過多") {
		t.Fatalf("expected registration rate limit message, got: %s", errText)
	}

	// Another session from a DIFFERENT IP -> MUST NOT be blocked!
	diffIP := "203.0.113.88"
	serverCtxDiff := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{
		ClientID:      "Guest",
		PrincipalType: "guest",
		SourceIP:      diffIP,
	})
	serverDiff := NewMCPServer(facade)
	clientDiff := mcp.NewClient(&mcp.Implementation{Name: "diff-tester", Version: "1"}, nil)
	serverTransportDiff, clientTransportDiff := mcp.NewInMemoryTransports()
	go serverDiff.Connect(serverCtxDiff, serverTransportDiff, nil)
	sessionDiff, err := clientDiff.Connect(context.Background(), clientTransportDiff, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionDiff.Close()

	resDiff, err := sessionDiff.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_request_registration",
		Arguments: map[string]any{
			"display_name": "DifferentSourceBot",
			"mac_address":  "AA:BB:CC:22:22:99",
		},
	})
	if err != nil || (resDiff != nil && resDiff.IsError) {
		t.Fatalf("expected registration from different IP to succeed, got: %v, %#v", err, resDiff)
	}
}

func TestAuthAPI_RateLimiting(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	store.AuthRateLimiter = NewAuthRateLimiter(2, 1*time.Minute, 5*time.Minute)
	store.RegRateLimiter = NewAuthRateLimiter(2, 1*time.Minute, 5*time.Minute)
	api := &HttpAPI_auth{Store: store}

	// 1. Test /auth/devRegister rate limiting
	regBody1 := `{"client_id":"dev-bot-1","display_name":"Bot1","mac_address":"11:22:33:44:55:01"}`
	req1, _ := http.NewRequest(http.MethodPost, "/auth/devRegister", strings.NewReader(regBody1))
	req1.RemoteAddr = "192.0.2.10:1234"
	respBytes1 := api.Process(nil, req1, nil, []string{"devRegister"}, nil, regBody1)
	var resp1 DevRegisterResponse
	_ = json.Unmarshal(respBytes1, &resp1)
	if !resp1.OK {
		t.Fatalf("expected 1st registration to succeed, got: %#v", resp1)
	}

	regBody2 := `{"client_id":"dev-bot-2","display_name":"Bot2","mac_address":"11:22:33:44:55:02"}`
	req2, _ := http.NewRequest(http.MethodPost, "/auth/devRegister", strings.NewReader(regBody2))
	req2.RemoteAddr = "192.0.2.10:1234"
	respBytes2 := api.Process(nil, req2, nil, []string{"devRegister"}, nil, regBody2)
	var resp2 DevRegisterResponse
	_ = json.Unmarshal(respBytes2, &resp2)
	if !resp2.OK {
		t.Fatalf("expected 2nd registration to succeed, got: %#v", resp2)
	}

	// 3rd registration from same IP should be blocked by RegRateLimiter
	regBody3 := `{"client_id":"dev-bot-3","display_name":"Bot3","mac_address":"11:22:33:44:55:03"}`
	req3, _ := http.NewRequest(http.MethodPost, "/auth/devRegister", strings.NewReader(regBody3))
	req3.RemoteAddr = "192.0.2.10:1234"
	respBytes3 := api.Process(nil, req3, nil, []string{"devRegister"}, nil, regBody3)
	var resp3 DevRegisterResponse
	_ = json.Unmarshal(respBytes3, &resp3)
	if resp3.OK || resp3.Reason != "rate_limited" {
		t.Fatalf("expected 3rd registration to be rate_limited, got: %#v", resp3)
	}

	// 2. Test /auth/devLogin rate limiting
	loginBodyFail := `{"client_id":"unknown-dev","mac_address":"99:99:99:99:99:99"}`
	loginReq1, _ := http.NewRequest(http.MethodPost, "/auth/devLogin", strings.NewReader(loginBodyFail))
	loginReq1.RemoteAddr = "192.0.2.20:1234"
	_ = api.Process(nil, loginReq1, nil, []string{"devLogin"}, nil, loginBodyFail)

	loginReq2, _ := http.NewRequest(http.MethodPost, "/auth/devLogin", strings.NewReader(loginBodyFail))
	loginReq2.RemoteAddr = "192.0.2.20:1234"
	_ = api.Process(nil, loginReq2, nil, []string{"devLogin"}, nil, loginBodyFail)

	// 3rd login attempt should be locked out
	loginReq3, _ := http.NewRequest(http.MethodPost, "/auth/devLogin", strings.NewReader(loginBodyFail))
	loginReq3.RemoteAddr = "192.0.2.20:1234"
	respLoginBytes3 := api.Process(nil, loginReq3, nil, []string{"devLogin"}, nil, loginBodyFail)
	var loginResp3 DevLoginResponse
	_ = json.Unmarshal(respLoginBytes3, &loginResp3)
	if loginResp3.OK || loginResp3.Reason != "rate_limited" {
		t.Fatalf("expected 3rd devLogin to be rate_limited, got: %#v", loginResp3)
	}
}

func TestAgentTokenDatabaseValidationAndRecord(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir, 20, false)

	// 1. Setup an approved agent in agentRegistry with a token, but NO entry in authTokens
	now := time.Now()
	clientID := "agent-hermes-test"
	token := "hermes-secret-token-12345"
	_, err := store.UpsertAgentRegistry(AgentRegistryUpsert{
		ClientID:    clientID,
		DisplayName: "Hermes Agent Test",
		MACAddress:  "00:11:22:33:44:55",
		LastSeenAt:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentIssuedToken(clientID, token, now, now.Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentApproval(clientID, true, now); err != nil {
		t.Fatal(err)
	}

	// 2. Incoming request with this token from IP 203.0.113.50
	r1, _ := http.NewRequest(http.MethodGet, "/mcp", nil)
	r1.RemoteAddr = "203.0.113.50:54321"
	r1.Header.Set("Authorization", "Bearer "+token)

	p1, ok1 := requireAuthorizedRequest(r1, nil, store)
	if !ok1 || p1 == nil {
		t.Fatalf("expected agent token to be validated from database, got ok=false")
	}
	if p1.ClientID != clientID || p1.PrincipalType != "agent" {
		t.Fatalf("unexpected principal: %+v", p1)
	}

	// Verify that the token was recorded in authTokens with the source IP
	rec1, found1 := store.GetAuthTokenRecord(token)
	if !found1 {
		t.Fatalf("expected token to be recorded in authTokens map")
	}
	if rec1.SourceIP != "203.0.113.50" {
		t.Fatalf("expected recorded source IP to be 203.0.113.50, got %s", rec1.SourceIP)
	}

	// 3. Incoming request with the same token from a CHANGED IP (e.g. Cloudflare / dynamic IP: 198.51.100.80)
	r2, _ := http.NewRequest(http.MethodGet, "/mcp", nil)
	r2.RemoteAddr = "198.51.100.80:54321"
	r2.Header.Set("Authorization", "Bearer "+token)

	p2, ok2 := requireAuthorizedRequest(r2, nil, store)
	if !ok2 || p2 == nil {
		t.Fatalf("expected agent token from changed IP to be validated and accepted, got ok=false")
	}
	if p2.ClientID != clientID {
		t.Fatalf("unexpected principal clientID: %s", p2.ClientID)
	}

	// Verify that the new source IP was recorded
	rec2, _ := store.GetAuthTokenRecord(token)
	if rec2.SourceIP != "198.51.100.80" {
		t.Fatalf("expected updated source IP to be 198.51.100.80, got %s", rec2.SourceIP)
	}

	// 4. Test persistent JWS RSA keys
	jwsDir := filepath.Join(tempDir, "jws_keys")
	initPersistentJWSKeys(jwsDir)
	pubFile := filepath.Join(jwsDir, "jws_rsa.pub")
	priFile := filepath.Join(jwsDir, "jws_rsa.pri")
	if _, err := os.Stat(pubFile); err != nil {
		t.Fatalf("expected persistent JWS public key to exist: %v", err)
	}
	if _, err := os.Stat(priFile); err != nil {
		t.Fatalf("expected persistent JWS private key to exist: %v", err)
	}

	// Reloading should succeed with the existing keys
	initPersistentJWSKeys(jwsDir)
}
