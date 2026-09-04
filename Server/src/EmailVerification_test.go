package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type memoryEmailSender struct {
	mu       sync.Mutex
	messages []EmailMessage
}

func TestMCPNewRegistrationUsesEmailChallenge(t *testing.T) {
	initPersistentJWSKeys(t.TempDir())
	store := NewStore(t.TempDir(), 20, false)
	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	if err := manager.SetDailyRegistrationLimit(1); err != nil {
		t.Fatal(err)
	}
	server := NewMCPServer(&SmallTalkFacade{Store: store, Email: manager})
	client := mcp.NewClient(&mcp.Implementation{Name: "email-registration-test", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(context.Background(), serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	requested, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"display_name": "Email MCP Agent", "email": "mcp@example.test",
	}})
	if err != nil || requested == nil || requested.IsError {
		t.Fatalf("registration request failed: result=%#v err=%v", requested, err)
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(requested.Content[0].(*mcp.TextContent).Text), &receipt); err != nil {
		t.Fatal(err)
	}
	clientID, _ := receipt["client_id"].(string)
	if clientID == "" || receipt["account_status"] != "not_created" || receipt["token_released"] != false {
		t.Fatalf("ambiguous unverified registration response: %#v", receipt)
	}
	if _, exists := store.GetAgentRegistry(clientID); exists {
		t.Fatal("MCP registration created an account before Email verification")
	}

	limited, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"display_name": "Quota Agent", "email": "quota@example.test",
	}})
	if err != nil || limited == nil || limited.IsError {
		t.Fatalf("daily limit must be a structured MCP result: result=%#v err=%v", limited, err)
	}
	var limitedReceipt map[string]any
	if err := json.Unmarshal([]byte(limited.Content[0].(*mcp.TextContent).Text), &limitedReceipt); err != nil {
		t.Fatal(err)
	}
	if limitedReceipt["status"] != "daily_registration_limit_reached" || limitedReceipt["email_sent"] != false || limitedReceipt["daily_registration_limit"] != float64(1) || limitedReceipt["retry_at"] == "" {
		t.Fatalf("MCP did not explain the exhausted daily registration quota: %#v", limitedReceipt)
	}

	agentURL := agentVerificationURLFromMessage(t, sender.last(t))
	completed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "smalltalk_complete_email_verification", Arguments: map[string]any{
		"verification_url": agentURL,
	}})
	if err != nil || completed == nil || completed.IsError {
		t.Fatalf("verification completion failed: result=%#v err=%v", completed, err)
	}
	var result EmailCompletionResult
	if err := json.Unmarshal([]byte(completed.Content[0].(*mcp.TextContent).Text), &result); err != nil {
		t.Fatal(err)
	}
	if !result.TokenReleased || result.AuthToken == "" || result.ClientID != clientID {
		t.Fatalf("verified MCP registration did not return one-time TOKEN: %#v", result)
	}
	reused, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "smalltalk_complete_email_verification", Arguments: map[string]any{
		"verification_url": agentURL,
	}})
	if err == nil && reused != nil && !reused.IsError {
		t.Fatalf("single-use Agent URL was accepted twice: %#v", reused)
	}
	if _, err := manager.CompleteURL(context.Background(), "https://evil.example/verify-agent.html#challenge_id=x&agent_token=y", ""); err == nil {
		t.Fatal("foreign-origin Agent verification URL was accepted")
	}
}

func TestEmailVerificationDoesNotResendSameRegistrationWithin24Hours(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	current := time.Date(2026, 9, 5, 9, 0, 0, 0, time.FixedZone("Asia/Taipei", 8*60*60))
	manager.now = func() time.Time { return current }

	first, err := manager.RequestRegistration(context.Background(), "agent-first", "Same Agent", "", "same@example.test", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.RequestRegistration(context.Background(), "agent-generated-again", "Same Agent", "", "SAME@example.test", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "verification_already_sent" || second.EmailSent || second.ChallengeID != first.ChallengeID || second.ClientID != first.ClientID || second.RetryAt == "" {
		t.Fatalf("duplicate request did not reuse the pending registration identity: first=%#v second=%#v", first, second)
	}
	sender.mu.Lock()
	messageCount := len(sender.messages)
	sender.mu.Unlock()
	if messageCount != 1 {
		t.Fatalf("duplicate request sent %d Emails, want 1", messageCount)
	}

	current = current.Add(emailResendCooldown + time.Minute)
	third, err := manager.RequestRegistration(context.Background(), "agent-after-cooldown", "Same Agent", "", "same@example.test", "192.0.2.10")
	if err != nil || !third.EmailSent || third.ChallengeID == first.ChallengeID {
		t.Fatalf("request after cooldown did not send a fresh challenge: receipt=%#v err=%v", third, err)
	}
}

func TestEmailDailyRegistrationLimitResetsNextLocalDay(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	if err := manager.SetDailyRegistrationLimit(2); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, 9, 5, 23, 55, 0, 0, time.FixedZone("Asia/Taipei", 8*60*60))
	manager.now = func() time.Time { return current }

	for index, item := range []struct{ id, name, email string }{
		{"agent-one", "Agent One", "one@example.test"},
		{"agent-two", "Agent Two", "two@example.test"},
	} {
		receipt, err := manager.RequestRegistration(context.Background(), item.id, item.name, "", item.email, "192.0.2.11")
		if err != nil || !receipt.EmailSent {
			t.Fatalf("registration %d failed: receipt=%#v err=%v", index+1, receipt, err)
		}
	}
	limited, err := manager.RequestRegistration(context.Background(), "agent-three", "Agent Three", "", "three@example.test", "192.0.2.11")
	if err != nil {
		t.Fatal(err)
	}
	if limited.Status != "daily_registration_limit_reached" || limited.EmailSent || limited.DailyRegistrationLimit != 2 || limited.RetryAt == "" {
		t.Fatalf("daily limit response is incomplete: %#v", limited)
	}

	current = current.Add(10 * time.Minute)
	nextDay, err := manager.RequestRegistration(context.Background(), "agent-three", "Agent Three", "", "three@example.test", "192.0.2.11")
	if err != nil || !nextDay.EmailSent {
		t.Fatalf("daily quota did not reset on the next local day: receipt=%#v err=%v", nextDay, err)
	}
}

func TestAgentVerificationHTTPUsesPOSTAndDoesNotRequireCode(t *testing.T) {
	initPersistentJWSKeys(t.TempDir())
	store := NewStore(t.TempDir(), 20, false)
	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	if _, err := manager.RequestRegistration(context.Background(), "agent-http", "HTTP Agent", "", "http-agent@example.test", "192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	agentURL := agentVerificationURLFromMessage(t, sender.last(t))
	parsed, err := url.Parse(agentURL)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	bodyBytes, _ := json.Marshal(EmailAgentCompleteRequest{ChallengeID: fragment.Get("challenge_id"), AgentToken: fragment.Get("agent_token")})
	body := string(bodyBytes)
	api := &HttpAPI_auth{Store: store, Email: manager}

	getRequest := httptest.NewRequest(http.MethodGet, "/auth/email/agent-complete", nil)
	getResponse := string(api.Process(httptest.NewRecorder(), getRequest, nil, []string{"email", "agent-complete"}, nil, ""))
	if !strings.Contains(getResponse, "post required") {
		t.Fatalf("GET did not remain non-mutating: %s", getResponse)
	}
	if _, exists := store.GetAgentRegistry("agent-http"); exists {
		t.Fatal("GET request consumed Agent verification URL")
	}

	postRequest := httptest.NewRequest(http.MethodPost, "/auth/email/agent-complete", strings.NewReader(body))
	var completed EmailCompletionResult
	if err := json.Unmarshal(api.Process(httptest.NewRecorder(), postRequest, nil, []string{"email", "agent-complete"}, nil, body), &completed); err != nil {
		t.Fatal(err)
	}
	if !completed.OK || !completed.TokenReleased || completed.AuthToken == "" {
		t.Fatalf("Agent POST completion failed: %#v", completed)
	}
}

func (s *memoryEmailSender) Send(_ context.Context, message EmailMessage) error {
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
	return nil
}

func (s *memoryEmailSender) last(t *testing.T) EmailMessage {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		t.Fatal("verification email was not sent")
	}
	return s.messages[len(s.messages)-1]
}

func emailProofFromMessage(t *testing.T, message EmailMessage) (string, string, string) {
	t.Helper()
	codeMatch := regexp.MustCompile(`人工驗證碼：([2-9A-HJ-NP-Z]{10})`).FindStringSubmatch(message.Text)
	urlMatch := regexp.MustCompile(`人工驗證連結：(https://\S+)`).FindStringSubmatch(message.Text)
	if len(codeMatch) != 2 || len(urlMatch) != 2 {
		t.Fatalf("verification email does not contain proof: %q", message.Text)
	}
	parsed, err := url.Parse(urlMatch[1])
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	return fragment.Get("challenge_id"), fragment.Get("link_token"), codeMatch[1]
}

func agentVerificationURLFromMessage(t *testing.T, message EmailMessage) string {
	t.Helper()
	match := regexp.MustCompile(`Agent 自動驗證 URL[^：]*：(https://\S+)`).FindStringSubmatch(message.Text)
	if len(match) != 2 {
		t.Fatalf("verification Email does not contain an Agent URL: %q", message.Text)
	}
	return match[1]
}

func newTestEmailManager(t *testing.T, store *Store, sender *memoryEmailSender) *EmailManager {
	t.Helper()
	secret := make([]byte, 64)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	manager, err := NewEmailManager(store, t.TempDir(), "https://bbs.example.test", secret[:32], secret[32:], sender)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestEmailRegistrationPreservesLegacyTokenAndCreatesNewAccountOnlyAfterVerification(t *testing.T) {
	initPersistentJWSKeys(t.TempDir())
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	legacy, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "legacy-agent", DisplayName: "Legacy Agent", LastSeenAt: now})
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := "legacy-token-must-remain-valid"
	if _, err = store.SetAgentIssuedToken(legacy.ClientID, legacyToken, now, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpsertAuthToken(AuthTokenRecord{Token: legacyToken, ClientID: legacy.ClientID, Kind: "dev-short", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano)}, true); err != nil {
		t.Fatal(err)
	}

	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	receipt, err := manager.RequestRegistration(context.Background(), "agent-new", "New Agent", "AABBCCDDEEFF", "new@example.test", "192.0.2.5")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "verification_required" {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, receipt.ExpiresAt)
	if err != nil || time.Until(expiresAt) < 23*time.Hour || time.Until(expiresAt) > 24*time.Hour+time.Minute {
		t.Fatalf("registration challenge TTL is not 24 hours: %q", receipt.ExpiresAt)
	}
	if _, exists := store.GetAgentRegistry("agent-new"); exists {
		t.Fatal("new account was created before Email verification")
	}
	if record, ok := store.AuthorizeAuthToken(legacyToken, ""); !ok || record.ClientID != legacy.ClientID {
		t.Fatal("legacy TOKEN became invalid after enabling Email registration")
	}

	challengeID, linkToken, code := emailProofFromMessage(t, sender.last(t))
	result, err := manager.Complete(context.Background(), challengeID, linkToken, code, "192.0.2.5")
	if err != nil {
		t.Fatal(err)
	}
	if !result.TokenReleased || result.AuthToken == "" || !result.WriteAccess {
		t.Fatalf("new registration did not release a usable TOKEN: %#v", result)
	}
	entry, exists := store.GetAgentRegistry("agent-new")
	if !exists || !entry.Approved || !entry.TokenIssued {
		t.Fatalf("verified account was not activated: %#v", entry)
	}
	if record, ok := store.AuthorizeAuthToken(result.AuthToken, ""); !ok || record.ClientID != "agent-new" {
		t.Fatal("new TOKEN cannot authenticate")
	}
	if confirmation := sender.last(t); strings.Contains(confirmation.Text, result.AuthToken) {
		t.Fatal("confirmation Email leaked the permanent TOKEN")
	}
	if state, err := os.ReadFile(manager.statePath); err != nil || strings.Contains(string(state), "new@example.test") {
		t.Fatalf("persisted verification state exposed plaintext Email: err=%v", err)
	}
	if _, err := manager.Complete(context.Background(), challengeID, linkToken, code, "192.0.2.5"); err == nil {
		t.Fatal("single-use challenge was accepted twice")
	}
}

func TestEmailBindingKeepsTokenAndRecoveryRotatesIt(t *testing.T) {
	initPersistentJWSKeys(t.TempDir())
	store := NewStore(t.TempDir(), 20, false)
	now := time.Now()
	entry, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "existing-agent", DisplayName: "Existing Agent", LastSeenAt: now})
	if err != nil {
		t.Fatal(err)
	}
	originalToken := "existing-token"
	if _, err = store.SetAgentIssuedToken(entry.ClientID, originalToken, now, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpsertAuthToken(AuthTokenRecord{Token: originalToken, ClientID: entry.ClientID, Kind: "dev-short", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano)}, true); err != nil {
		t.Fatal(err)
	}

	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	if _, err := manager.RequestBinding(context.Background(), entry.ClientID, "owner@example.test", "192.0.2.8"); err != nil {
		t.Fatal(err)
	}
	challengeID, linkToken, code := emailProofFromMessage(t, sender.last(t))
	bound, err := manager.Complete(context.Background(), challengeID, linkToken, code, "192.0.2.8")
	if err != nil {
		t.Fatal(err)
	}
	if bound.TokenReleased || bound.AuthToken != "" {
		t.Fatalf("binding unexpectedly revealed or changed TOKEN: %#v", bound)
	}
	if current, _ := store.GetAgentRegistry(entry.ClientID); current.Token != originalToken {
		t.Fatal("Email binding changed the existing TOKEN")
	}

	manager.RequestRecovery(context.Background(), entry.ClientID, "owner@example.test", "192.0.2.8")
	challengeID, linkToken, code = emailProofFromMessage(t, sender.last(t))
	manager.mu.Lock()
	recoveryChallenge := manager.state.Challenges[challengeID]
	manager.mu.Unlock()
	recoveryExpiresAt, err := time.Parse(time.RFC3339Nano, recoveryChallenge.ExpiresAt)
	if err != nil || time.Until(recoveryExpiresAt) < 14*time.Minute || time.Until(recoveryExpiresAt) > 16*time.Minute {
		t.Fatalf("recovery challenge TTL is not 15 minutes: %q", recoveryChallenge.ExpiresAt)
	}
	recovered, err := manager.Complete(context.Background(), challengeID, linkToken, code, "192.0.2.8")
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.TokenRotated || recovered.AuthToken == "" || recovered.AuthToken == originalToken {
		t.Fatalf("recovery did not rotate TOKEN: %#v", recovered)
	}
	if _, ok := store.AuthorizeAuthToken(originalToken, ""); ok {
		t.Fatal("old TOKEN remained valid after recovery")
	}
	if _, ok := store.AuthorizeAuthToken(recovered.AuthToken, ""); !ok {
		t.Fatal("rotated TOKEN is not valid")
	}
	if confirmation := sender.last(t); strings.Contains(confirmation.Text, recovered.AuthToken) {
		t.Fatal("recovery confirmation Email leaked the replacement TOKEN")
	}
}

func TestEmailBindingLimitIsFiveAccounts(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	email := "shared@example.test"
	encrypted, err := manager.encryptEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	for i := 0; i < emailBindingLimit; i++ {
		id := "agent-" + string(rune('a'+i))
		manager.state.Bindings[id] = EmailBinding{ClientID: id, EmailHash: manager.emailHash(email), EmailEncrypted: encrypted, VerifiedAt: time.Now().Format(time.RFC3339Nano)}
	}
	manager.mu.Unlock()
	_, err = manager.RequestRegistration(context.Background(), "agent-six", "Sixth Agent", "", email, "192.0.2.9")
	if err == nil || !strings.Contains(err.Error(), "at most 5") {
		t.Fatalf("sixth account was not rejected: %v", err)
	}
}
