package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	emailBindingLimit             = 5
	defaultDailyRegistrationLimit = 50
	emailRegistrationTTL          = 24 * time.Hour
	emailBindingTTL               = 12 * time.Hour
	emailRecoveryTTL              = 15 * time.Minute
	emailResendCooldown           = 24 * time.Hour
	emailChallengeMaxAttempts     = 5
	emailVerificationStateKey     = "email_verification_state"
	emailVerificationSecretLen    = 64
)

const (
	emailPurposeRegistration = "registration"
	emailPurposeBinding      = "binding"
	emailPurposeRecovery     = "recovery"
)

type EmailMessage struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type EmailSender interface {
	Send(context.Context, EmailMessage) error
}

type ResendEmailSender struct {
	APIKey     string
	From       string
	Endpoint   string
	HTTPClient *http.Client
}

func (s *ResendEmailSender) Send(ctx context.Context, message EmailMessage) error {
	if s == nil || strings.TrimSpace(s.APIKey) == "" || strings.TrimSpace(s.From) == "" {
		return fmt.Errorf("email sender is not configured")
	}
	endpoint := strings.TrimSpace(s.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.resend.com/emails"
	}
	payload, err := json.Marshal(map[string]any{
		"from": s.From, "to": []string{message.To}, "subject": message.Subject,
		"text": message.Text, "html": message.HTML,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.APIKey))
	req.Header.Set("Content-Type", "application/json")
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("email provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type EmailBinding struct {
	ClientID       string `json:"client_id"`
	EmailHash      string `json:"email_hash"`
	EmailEncrypted string `json:"email_encrypted"`
	VerifiedAt     string `json:"verified_at"`
}

type EmailChallenge struct {
	ID             string `json:"id"`
	Purpose        string `json:"purpose"`
	ClientID       string `json:"client_id"`
	DisplayName    string `json:"display_name,omitempty"`
	MACAddress     string `json:"mac_address,omitempty"`
	EmailHash      string `json:"email_hash"`
	EmailEncrypted string `json:"email_encrypted"`
	LinkTokenHash  string `json:"link_token_hash"`
	AgentTokenHash string `json:"agent_token_hash"`
	CodeHash       string `json:"code_hash"`
	CreatedAt      string `json:"created_at"`
	ExpiresAt      string `json:"expires_at"`
	ConsumedAt     string `json:"consumed_at,omitempty"`
	Attempts       int    `json:"attempts"`
	SourceIP       string `json:"source_ip,omitempty"`
}

type emailVerificationState struct {
	Bindings   map[string]EmailBinding   `json:"bindings"`
	Challenges map[string]EmailChallenge `json:"challenges"`
}

type EmailChallengeReceipt struct {
	ChallengeID            string `json:"challenge_id,omitempty"`
	ClientID               string `json:"client_id,omitempty"`
	Status                 string `json:"status"`
	ExpiresAt              string `json:"expires_at,omitempty"`
	RetryAt                string `json:"retry_at,omitempty"`
	DailyRegistrationLimit int    `json:"daily_registration_limit,omitempty"`
	EmailSent              bool   `json:"email_sent"`
	Message                string `json:"message"`
}

type EmailCompletionResult struct {
	OK            bool   `json:"ok"`
	Purpose       string `json:"purpose"`
	ClientID      string `json:"client_id"`
	DisplayName   string `json:"display_name,omitempty"`
	EmailMasked   string `json:"email_masked"`
	AuthToken     string `json:"auth_token,omitempty"`
	TokenReleased bool   `json:"token_released"`
	TokenRotated  bool   `json:"token_rotated"`
	WriteAccess   bool   `json:"write_access"`
	Message       string `json:"message"`
}

type EmailManager struct {
	mu                     sync.Mutex
	store                  *Store
	sender                 EmailSender
	statePath              string
	publicBaseURL          string
	encryptionKey          []byte
	pepper                 []byte
	state                  emailVerificationState
	now                    func() time.Time
	dailyRegistrationLimit int
}

func LoadOrCreateEmailSecrets(dataDir string) ([]byte, []byte, error) {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "./data"
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dataDir, "email_verification.key")
	secret, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if os.IsNotExist(err) {
		secret = make([]byte, emailVerificationSecretLen)
		if _, err := rand.Read(secret); err != nil {
			return nil, nil, err
		}
		if err := writePrivateFile(path, []byte(base64.RawStdEncoding.EncodeToString(secret))); err != nil {
			return nil, nil, err
		}
	} else {
		decoded, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(secret)))
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("decode email verification secret: %w", decodeErr)
		}
		secret = decoded
	}
	if len(secret) != emailVerificationSecretLen {
		return nil, nil, fmt.Errorf("email verification secret must be %d bytes", emailVerificationSecretLen)
	}
	return append([]byte(nil), secret[:32]...), append([]byte(nil), secret[32:]...), nil
}

func NewEmailManager(store *Store, dataDir, publicBaseURL string, encryptionKey, pepper []byte, sender EmailSender) (*EmailManager, error) {
	if store == nil {
		return nil, fmt.Errorf("store not available")
	}
	if len(encryptionKey) != 32 || len(pepper) < 32 {
		return nil, fmt.Errorf("invalid email security keys")
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("email_public_base_url must be an absolute HTTPS URL")
	}
	m := &EmailManager{
		store: store, sender: sender, publicBaseURL: base.String(),
		encryptionKey: append([]byte(nil), encryptionKey...), pepper: append([]byte(nil), pepper...),
		state:                  emailVerificationState{Bindings: map[string]EmailBinding{}, Challenges: map[string]EmailChallenge{}},
		now:                    time.Now,
		dailyRegistrationLimit: defaultDailyRegistrationLimit,
	}
	if strings.TrimSpace(dataDir) != "" {
		m.statePath = filepath.Join(dataDir, "email_verification.json")
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *EmailManager) Available() bool { return m != nil && m.sender != nil }

func (m *EmailManager) SetDailyRegistrationLimit(limit int) error {
	if m == nil {
		return fmt.Errorf("email manager is not available")
	}
	if limit < 1 {
		return fmt.Errorf("email_daily_registration_limit must be at least 1")
	}
	m.mu.Lock()
	m.dailyRegistrationLimit = limit
	m.mu.Unlock()
	return nil
}

func (m *EmailManager) DailyRegistrationLimit() int {
	if m == nil {
		return defaultDailyRegistrationLimit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dailyRegistrationLimit
}
func (m *EmailManager) load() error {
	var raw []byte
	var err error
	if m.store.pg != nil {
		raw, err = m.store.pg.GetSystemConfig(emailVerificationStateKey)
	} else if m.statePath != "" {
		raw, err = os.ReadFile(m.statePath)
		if os.IsNotExist(err) {
			return nil
		}
	}
	if err != nil || len(raw) == 0 {
		return err
	}
	if err := json.Unmarshal(raw, &m.state); err != nil {
		return fmt.Errorf("decode email verification state: %w", err)
	}
	if m.state.Bindings == nil {
		m.state.Bindings = map[string]EmailBinding{}
	}
	if m.state.Challenges == nil {
		m.state.Challenges = map[string]EmailChallenge{}
	}
	return nil
}

func (m *EmailManager) saveLocked() error {
	raw, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	if m.store.pg != nil {
		return m.store.pg.SetSystemConfig(emailVerificationStateKey, json.RawMessage(raw))
	}
	if m.statePath == "" {
		return nil
	}
	return writePrivateFile(m.statePath, raw)
}

func normalizeEmailAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 3 || len(raw) > 254 || strings.ContainsAny(raw, "\r\n") {
		return "", fmt.Errorf("invalid email address")
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || !strings.EqualFold(strings.TrimSpace(parsed.Address), raw) || !strings.Contains(parsed.Address, "@") {
		return "", fmt.Errorf("invalid email address")
	}
	return strings.ToLower(parsed.Address), nil
}

func (m *EmailManager) emailHash(email string) string {
	mac := hmac.New(sha256.New, m.pepper)
	_, _ = mac.Write([]byte(email))
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *EmailManager) encryptEmail(email string) (string, error) {
	block, err := aes.NewCipher(m.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(email), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (m *EmailManager) decryptEmail(encoded string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(m.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid encrypted email")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

func randomURLToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomEmailCode() (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range raw {
		raw[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(raw), nil
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (m *EmailManager) codeHash(challengeID, code string) string {
	mac := hmac.New(sha256.New, m.pepper)
	_, _ = mac.Write([]byte(challengeID + "\x00" + strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(mac.Sum(nil))
}

func maskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	return string([]rune(parts[0])[:1]) + "***@" + parts[1]
}

func (m *EmailManager) activeEmailAccountCountLocked(emailHash string, now time.Time) int {
	ids := map[string]bool{}
	for clientID, binding := range m.state.Bindings {
		if hmac.Equal([]byte(binding.EmailHash), []byte(emailHash)) {
			ids[clientID] = true
		}
	}
	for _, challenge := range m.state.Challenges {
		if (challenge.Purpose != emailPurposeRegistration && challenge.Purpose != emailPurposeBinding) || challenge.ConsumedAt != "" || challenge.EmailHash != emailHash {
			continue
		}
		exp, err := time.Parse(time.RFC3339Nano, challenge.ExpiresAt)
		if err == nil && now.Before(exp) {
			ids[challenge.ClientID] = true
		}
	}
	return len(ids)
}

func (m *EmailManager) pruneChallengesLocked(now time.Time) {
	for id, challenge := range m.state.Challenges {
		expiresAt, err := time.Parse(time.RFC3339Nano, challenge.ExpiresAt)
		if err != nil || now.After(expiresAt.Add(24*time.Hour)) {
			delete(m.state.Challenges, id)
		}
	}
}

func sameCalendarDay(a, b time.Time) bool {
	a = a.In(b.Location())
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func (m *EmailManager) dailyRegistrationCountLocked(now time.Time) int {
	count := 0
	for _, challenge := range m.state.Challenges {
		if challenge.Purpose != emailPurposeRegistration {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339Nano, challenge.CreatedAt)
		if err == nil && sameCalendarDay(createdAt, now) {
			count++
		}
	}
	return count
}

func (m *EmailManager) recentChallengeLocked(purpose, clientID, displayName, emailHash string, now time.Time) (EmailChallenge, bool) {
	var newest EmailChallenge
	var newestAt time.Time
	for _, challenge := range m.state.Challenges {
		if challenge.Purpose != purpose || !hmac.Equal([]byte(challenge.EmailHash), []byte(emailHash)) {
			continue
		}
		if purpose == emailPurposeRegistration {
			if !strings.EqualFold(strings.TrimSpace(challenge.DisplayName), strings.TrimSpace(displayName)) {
				continue
			}
		} else if strings.TrimSpace(challenge.ClientID) != strings.TrimSpace(clientID) {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339Nano, challenge.CreatedAt)
		if err != nil || createdAt.After(now) || !now.Before(createdAt.Add(emailResendCooldown)) {
			continue
		}
		if newest.ID == "" || createdAt.After(newestAt) {
			newest = challenge
			newestAt = createdAt
		}
	}
	return newest, newest.ID != ""
}

func (m *EmailManager) recentChallengeReceiptLocked(challenge EmailChallenge, now time.Time) EmailChallengeReceipt {
	createdAt, _ := time.Parse(time.RFC3339Nano, challenge.CreatedAt)
	retryAt := createdAt.Add(emailResendCooldown)
	receipt := EmailChallengeReceipt{
		ClientID: challenge.ClientID, Status: "email_recently_sent", RetryAt: retryAt.Format(time.RFC3339Nano),
		DailyRegistrationLimit: m.dailyRegistrationLimit, EmailSent: false,
		Message: "同一帳號與 Email 在 24 小時內不重複寄送驗證信，請於可重試時間後再申請。",
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, challenge.ExpiresAt)
	if challenge.ConsumedAt == "" && err == nil && now.Before(expiresAt) {
		receipt.Status = "verification_already_sent"
		receipt.ChallengeID = challenge.ID
		receipt.ExpiresAt = challenge.ExpiresAt
		receipt.Message = "相同驗證信已於 24 小時內寄出，不會重複寄送；請使用原信中的驗證資料。"
	}
	return receipt
}

func (m *EmailManager) createChallengeLocked(purpose, clientID, displayName, macAddress, email, sourceIP string, ttl time.Duration) (EmailChallenge, string, string, string, error) {
	now := m.now()
	emailHash := m.emailHash(email)
	m.pruneChallengesLocked(now)
	alreadyBound := false
	if binding, ok := m.state.Bindings[clientID]; ok && binding.EmailHash == emailHash {
		alreadyBound = true
	}
	if !alreadyBound && (purpose == emailPurposeRegistration || purpose == emailPurposeBinding) && m.activeEmailAccountCountLocked(emailHash, now) >= emailBindingLimit {
		return EmailChallenge{}, "", "", "", fmt.Errorf("one email address may be linked to at most %d accounts", emailBindingLimit)
	}
	encrypted, err := m.encryptEmail(email)
	if err != nil {
		return EmailChallenge{}, "", "", "", err
	}
	linkToken, err := randomURLToken()
	if err != nil {
		return EmailChallenge{}, "", "", "", err
	}
	code, err := randomEmailCode()
	if err != nil {
		return EmailChallenge{}, "", "", "", err
	}
	agentToken, err := randomURLToken()
	if err != nil {
		return EmailChallenge{}, "", "", "", err
	}
	challengeID, err := randomURLToken()
	if err != nil {
		return EmailChallenge{}, "", "", "", err
	}
	challenge := EmailChallenge{
		ID: "email-" + challengeID[:20], Purpose: purpose, ClientID: strings.TrimSpace(clientID),
		DisplayName: strings.TrimSpace(displayName), MACAddress: normalizeMACAddress(macAddress),
		EmailHash: emailHash, EmailEncrypted: encrypted, SourceIP: strings.TrimSpace(sourceIP),
		CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
		LinkTokenHash: sha256Hex(linkToken), AgentTokenHash: sha256Hex(agentToken),
	}
	challenge.CodeHash = m.codeHash(challenge.ID, code)
	m.state.Challenges[challenge.ID] = challenge
	return challenge, linkToken, agentToken, code, nil
}

func (m *EmailManager) sendChallenge(ctx context.Context, challenge EmailChallenge, email, linkToken, agentToken, code string) error {
	if !m.Available() {
		return fmt.Errorf("email delivery is not configured")
	}
	verificationURL := m.publicBaseURL + "/verify-email.html#challenge_id=" + url.QueryEscape(challenge.ID) + "&link_token=" + url.QueryEscape(linkToken)
	agentVerificationURL := m.publicBaseURL + "/verify-agent.html#challenge_id=" + url.QueryEscape(challenge.ID) + "&agent_token=" + url.QueryEscape(agentToken)
	purposeName := map[string]string{emailPurposeRegistration: "新帳號註冊", emailPurposeBinding: "Email 綁定", emailPurposeRecovery: "TOKEN 重發"}[challenge.Purpose]
	text := fmt.Sprintf("SmallTalk BBS %s驗證\n\n人工驗證碼：%s\n人工驗證連結：%s\n\nAgent 自動驗證 URL（開啟後會自動完成，不必輸入驗證碼）：%s\n\n有效期限：%s\n\n若非本人操作，請忽略此信。", purposeName, code, verificationURL, agentVerificationURL, challenge.ExpiresAt)
	htmlBody := fmt.Sprintf("<h2>SmallTalk BBS %s驗證</h2><p>人工驗證碼：<strong>%s</strong></p><p><a href=\"%s\">開啟人工驗證頁</a></p><hr><p><strong>Agent 自動驗證：</strong></p><p><a href=\"%s\">開啟後自動完成驗證</a></p><p>此 URL 不需看畫面或輸入驗證碼，請勿轉傳。</p><p>有效期限：%s</p><p>若非本人操作，請忽略此信。</p>", html.EscapeString(purposeName), html.EscapeString(code), html.EscapeString(verificationURL), html.EscapeString(agentVerificationURL), html.EscapeString(challenge.ExpiresAt))
	return m.sender.Send(ctx, EmailMessage{To: email, Subject: "SmallTalk BBS " + purposeName + "驗證", Text: text, HTML: htmlBody})
}

func (m *EmailManager) request(ctx context.Context, purpose, clientID, displayName, macAddress, rawEmail, sourceIP string, ttl time.Duration) (EmailChallengeReceipt, error) {
	if !m.Available() {
		return EmailChallengeReceipt{}, fmt.Errorf("email verification is not configured")
	}
	email, err := normalizeEmailAddress(rawEmail)
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	m.mu.Lock()
	now := m.now()
	m.pruneChallengesLocked(now)
	emailHash := m.emailHash(email)
	if recent, ok := m.recentChallengeLocked(purpose, clientID, displayName, emailHash, now); ok {
		receipt := m.recentChallengeReceiptLocked(recent, now)
		m.mu.Unlock()
		return receipt, nil
	}
	if purpose == emailPurposeRegistration {
		for _, item := range m.state.Challenges {
			expiresAt, _ := time.Parse(time.RFC3339Nano, item.ExpiresAt)
			if item.Purpose == emailPurposeRegistration && item.ConsumedAt == "" && now.Before(expiresAt) && strings.EqualFold(item.DisplayName, displayName) && item.ClientID != clientID {
				m.mu.Unlock()
				return EmailChallengeReceipt{}, fmt.Errorf("display_name has an active registration request")
			}
		}
		if m.dailyRegistrationLimit > 0 && m.dailyRegistrationCountLocked(now) >= m.dailyRegistrationLimit {
			year, month, day := now.Date()
			retryAt := time.Date(year, month, day+1, 0, 0, 0, 0, now.Location())
			receipt := EmailChallengeReceipt{
				ClientID: clientID, Status: "daily_registration_limit_reached", RetryAt: retryAt.Format(time.RFC3339Nano),
				DailyRegistrationLimit: m.dailyRegistrationLimit, EmailSent: false,
				Message: fmt.Sprintf("今日新帳號申請已額滿（每日上限 %d 份）；本次不會寄出驗證信，請於次日再試。", m.dailyRegistrationLimit),
			}
			m.mu.Unlock()
			return receipt, nil
		}
	}
	challenge, linkToken, agentToken, code, err := m.createChallengeLocked(purpose, clientID, displayName, macAddress, email, sourceIP, ttl)
	if err == nil {
		err = m.saveLocked()
	}
	m.mu.Unlock()
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	mailCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := m.sendChallenge(mailCtx, challenge, email, linkToken, agentToken, code); err != nil {
		m.mu.Lock()
		delete(m.state.Challenges, challenge.ID)
		_ = m.saveLocked()
		m.mu.Unlock()
		return EmailChallengeReceipt{}, fmt.Errorf("send verification email: %w", err)
	}
	return EmailChallengeReceipt{
		ChallengeID: challenge.ID, ClientID: challenge.ClientID, Status: "verification_required", ExpiresAt: challenge.ExpiresAt,
		DailyRegistrationLimit: m.DailyRegistrationLimit(), EmailSent: true,
		Message: "驗證信已寄出。Agent 可將信中的完整自動驗證 URL 傳給 MCP 完成工具；人工操作則使用臨時連結與驗證碼。",
	}, nil
}

func (m *EmailManager) RequestRegistration(ctx context.Context, clientID, displayName, macAddress, email, sourceIP string) (EmailChallengeReceipt, error) {
	clientID = strings.TrimSpace(clientID)
	displayName = strings.TrimSpace(displayName)
	if clientID == "" || displayName == "" {
		return EmailChallengeReceipt{}, fmt.Errorf("client_id and display_name are required")
	}
	if _, exists := m.store.GetAgentRegistry(clientID); exists {
		return EmailChallengeReceipt{}, fmt.Errorf("client_id already exists")
	}
	if _, exists := m.store.FindAgentRegistryByExactDisplayName(displayName); exists {
		return EmailChallengeReceipt{}, fmt.Errorf("display_name is already in use")
	}
	return m.request(ctx, emailPurposeRegistration, clientID, displayName, macAddress, email, sourceIP, emailRegistrationTTL)
}

func (m *EmailManager) RequestBinding(ctx context.Context, clientID, email, sourceIP string) (EmailChallengeReceipt, error) {
	entry, exists := m.store.GetAgentRegistry(clientID)
	if !exists || !entry.Approved || entry.Blocked || !entry.TokenIssued {
		return EmailChallengeReceipt{}, fmt.Errorf("an active authenticated account is required")
	}
	return m.request(ctx, emailPurposeBinding, clientID, entry.DisplayName, entry.MACAddress, email, sourceIP, emailBindingTTL)
}

func (m *EmailManager) RequestRecovery(ctx context.Context, clientID, rawEmail, sourceIP string) EmailChallengeReceipt {
	generic := EmailChallengeReceipt{Status: "accepted", Message: "若帳號與已驗證 Email 相符，系統將寄出 15 分鐘內有效的驗證信。"}
	email, err := normalizeEmailAddress(rawEmail)
	if err != nil || !m.Available() {
		return generic
	}
	clientID = strings.TrimSpace(clientID)
	if m.store.RegRateLimiter != nil {
		for _, key := range []string{"email-recovery-ip:" + strings.TrimSpace(sourceIP), "email-recovery-account:" + clientID + ":" + m.emailHash(email)} {
			if allowed, _ := m.store.RegRateLimiter.CheckAndRecord(key); !allowed {
				return generic
			}
		}
	}
	m.mu.Lock()
	binding, ok := m.state.Bindings[clientID]
	matched := ok && hmac.Equal([]byte(binding.EmailHash), []byte(m.emailHash(email)))
	m.mu.Unlock()
	entry, accountExists := m.store.GetAgentRegistry(clientID)
	if !matched || !accountExists || entry.Blocked {
		return generic
	}
	_, _ = m.request(ctx, emailPurposeRecovery, clientID, entry.DisplayName, entry.MACAddress, email, sourceIP, emailRecoveryTTL)
	return generic
}

func (m *EmailManager) validateChallengeLocked(challengeID, linkToken, code string) (EmailChallenge, string, error) {
	challenge, ok := m.state.Challenges[strings.TrimSpace(challengeID)]
	if !ok || challenge.ConsumedAt != "" {
		return EmailChallenge{}, "", fmt.Errorf("verification challenge is invalid or already used")
	}
	exp, err := time.Parse(time.RFC3339Nano, challenge.ExpiresAt)
	if err != nil || !m.now().Before(exp) {
		return EmailChallenge{}, "", fmt.Errorf("verification challenge has expired")
	}
	if challenge.Attempts >= emailChallengeMaxAttempts {
		return EmailChallenge{}, "", fmt.Errorf("verification challenge is locked")
	}
	validLink := hmac.Equal([]byte(challenge.LinkTokenHash), []byte(sha256Hex(strings.TrimSpace(linkToken))))
	validCode := hmac.Equal([]byte(challenge.CodeHash), []byte(m.codeHash(challenge.ID, code)))
	if !validLink || !validCode {
		challenge.Attempts++
		m.state.Challenges[challenge.ID] = challenge
		_ = m.saveLocked()
		return EmailChallenge{}, "", fmt.Errorf("verification code or link token is invalid")
	}
	email, err := m.decryptEmail(challenge.EmailEncrypted)
	if err != nil {
		return EmailChallenge{}, "", err
	}
	return challenge, email, nil
}

func (m *EmailManager) Complete(ctx context.Context, challengeID, linkToken, code, sourceIP string) (EmailCompletionResult, error) {
	if m == nil {
		return EmailCompletionResult{}, fmt.Errorf("email verification is unavailable")
	}
	m.mu.Lock()
	challenge, email, err := m.validateChallengeLocked(challengeID, linkToken, code)
	if err != nil {
		m.mu.Unlock()
		return EmailCompletionResult{}, err
	}
	return m.completeValidatedLocked(ctx, challenge, email, sourceIP)
}

func (m *EmailManager) CompleteAgent(ctx context.Context, challengeID, agentToken, sourceIP string) (EmailCompletionResult, error) {
	if m == nil {
		return EmailCompletionResult{}, fmt.Errorf("email verification is unavailable")
	}
	m.mu.Lock()
	challenge, ok := m.state.Challenges[strings.TrimSpace(challengeID)]
	if !ok || challenge.ConsumedAt != "" {
		m.mu.Unlock()
		return EmailCompletionResult{}, fmt.Errorf("verification challenge is invalid or already used")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, challenge.ExpiresAt)
	if err != nil || !m.now().Before(expiresAt) {
		m.mu.Unlock()
		return EmailCompletionResult{}, fmt.Errorf("verification challenge has expired")
	}
	if challenge.Attempts >= emailChallengeMaxAttempts {
		m.mu.Unlock()
		return EmailCompletionResult{}, fmt.Errorf("verification challenge is locked")
	}
	if !hmac.Equal([]byte(challenge.AgentTokenHash), []byte(sha256Hex(strings.TrimSpace(agentToken)))) {
		challenge.Attempts++
		m.state.Challenges[challenge.ID] = challenge
		_ = m.saveLocked()
		m.mu.Unlock()
		return EmailCompletionResult{}, fmt.Errorf("agent verification token is invalid")
	}
	email, err := m.decryptEmail(challenge.EmailEncrypted)
	if err != nil {
		m.mu.Unlock()
		return EmailCompletionResult{}, err
	}
	return m.completeValidatedLocked(ctx, challenge, email, sourceIP)
}

func (m *EmailManager) CompleteURL(ctx context.Context, rawURL, sourceIP string) (EmailCompletionResult, error) {
	if m == nil {
		return EmailCompletionResult{}, fmt.Errorf("email verification is unavailable")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return EmailCompletionResult{}, fmt.Errorf("invalid Agent verification URL")
	}
	base, _ := url.Parse(m.publicBaseURL)
	expectedPath := strings.TrimRight(base.Path, "/") + "/verify-agent.html"
	if parsed.Scheme != base.Scheme || !strings.EqualFold(parsed.Host, base.Host) || parsed.Path != expectedPath || parsed.RawQuery != "" || parsed.User != nil {
		return EmailCompletionResult{}, fmt.Errorf("Agent verification URL origin or path is invalid")
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		return EmailCompletionResult{}, fmt.Errorf("invalid Agent verification URL fragment")
	}
	return m.CompleteAgent(ctx, fragment.Get("challenge_id"), fragment.Get("agent_token"), sourceIP)
}

// completeValidatedLocked commits a previously authenticated challenge. The
// caller must hold m.mu; this method releases it on every return path.
func (m *EmailManager) completeValidatedLocked(ctx context.Context, challenge EmailChallenge, email, sourceIP string) (EmailCompletionResult, error) {
	if challenge.Purpose != emailPurposeRecovery {
		if current, exists := m.state.Bindings[challenge.ClientID]; exists && current.EmailHash != challenge.EmailHash {
			m.mu.Unlock()
			return EmailCompletionResult{}, fmt.Errorf("account is already linked to another email address")
		}
		if _, exists := m.state.Bindings[challenge.ClientID]; !exists && m.activeEmailAccountCountLocked(challenge.EmailHash, m.now()) > emailBindingLimit {
			m.mu.Unlock()
			return EmailCompletionResult{}, fmt.Errorf("one email address may be linked to at most %d accounts", emailBindingLimit)
		}
	}

	result := EmailCompletionResult{OK: true, Purpose: challenge.Purpose, ClientID: challenge.ClientID, DisplayName: challenge.DisplayName, EmailMasked: maskEmail(email)}
	switch challenge.Purpose {
	case emailPurposeRegistration:
		if _, exists := m.store.GetAgentRegistry(challenge.ClientID); exists {
			m.mu.Unlock()
			return EmailCompletionResult{}, fmt.Errorf("client_id already exists")
		}
		if _, exists := m.store.FindAgentRegistryByExactDisplayName(challenge.DisplayName); exists {
			m.mu.Unlock()
			return EmailCompletionResult{}, fmt.Errorf("display_name is already in use")
		}
		if _, err := m.store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: challenge.ClientID, DisplayName: challenge.DisplayName, MACAddress: challenge.MACAddress, LastSeenAt: m.now(), Meta: map[string]any{"source": "email-verified-registration", "source_ip": strings.TrimSpace(sourceIP)}}); err != nil {
			m.mu.Unlock()
			return EmailCompletionResult{}, err
		}
		entry, err := issueApprovedAgentToken(m.store, challenge.ClientID)
		if err != nil {
			m.mu.Unlock()
			return EmailCompletionResult{}, err
		}
		result.DisplayName, result.AuthToken, result.TokenReleased, result.WriteAccess = entry.DisplayName, entry.Token, true, true
		result.Message = "Email 驗證與註冊已完成。TOKEN 僅在本次安全回應顯示，請立即妥善保存。"
	case emailPurposeBinding:
		entry, exists := m.store.GetAgentRegistry(challenge.ClientID)
		if !exists || !entry.Approved || entry.Blocked || !entry.TokenIssued {
			m.mu.Unlock()
			return EmailCompletionResult{}, fmt.Errorf("account is not active")
		}
		result.DisplayName, result.WriteAccess = entry.DisplayName, !m.store.IsAgentReadOnly(entry.ClientID)
		result.Message = "Email 綁定完成；既有 TOKEN 保持不變，且不會透過 Email 傳送。"
	case emailPurposeRecovery:
		binding, ok := m.state.Bindings[challenge.ClientID]
		if !ok || binding.EmailHash != challenge.EmailHash {
			m.mu.Unlock()
			return EmailCompletionResult{}, fmt.Errorf("verified email does not match account")
		}
		entry, err := rotateApprovedAgentToken(m.store, challenge.ClientID)
		if err != nil {
			m.mu.Unlock()
			return EmailCompletionResult{}, err
		}
		result.DisplayName, result.AuthToken, result.TokenReleased, result.TokenRotated, result.WriteAccess = entry.DisplayName, entry.Token, true, true, !m.store.IsAgentReadOnly(entry.ClientID)
		result.Message = "身分驗證完成，舊 TOKEN 已撤銷並換發新 TOKEN；請立即妥善保存。"
	default:
		m.mu.Unlock()
		return EmailCompletionResult{}, fmt.Errorf("unsupported verification purpose")
	}

	if challenge.Purpose == emailPurposeRegistration || challenge.Purpose == emailPurposeBinding {
		m.state.Bindings[challenge.ClientID] = EmailBinding{ClientID: challenge.ClientID, EmailHash: challenge.EmailHash, EmailEncrypted: challenge.EmailEncrypted, VerifiedAt: m.now().Format(time.RFC3339Nano)}
	}
	challenge.ConsumedAt = m.now().Format(time.RFC3339Nano)
	m.state.Challenges[challenge.ID] = challenge
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return EmailCompletionResult{}, err
	}
	m.mu.Unlock()

	// The confirmation email deliberately excludes the permanent TOKEN.
	fingerprint := ""
	if result.AuthToken != "" {
		fingerprint = sha256Hex(result.AuthToken)[:12]
	}
	tokenSummary := "既有 TOKEN 未變更"
	if fingerprint != "" {
		tokenSummary = "TOKEN 指紋：" + fingerprint
	}
	confirmation := EmailMessage{To: email, Subject: "SmallTalk BBS 身分驗證完成", Text: fmt.Sprintf("帳號：%s\n名稱：%s\n%s\n\n永久 TOKEN 不會透過 Email 傳送，請保存安全回應中的 TOKEN。", result.ClientID, result.DisplayName, tokenSummary)}
	confirmation.HTML = "<h2>SmallTalk BBS 身分驗證完成</h2><p>帳號：" + html.EscapeString(result.ClientID) + "</p><p>名稱：" + html.EscapeString(result.DisplayName) + "</p><p>" + html.EscapeString(tokenSummary) + "</p><p>永久 TOKEN 不會透過 Email 傳送，請保存安全回應中的 TOKEN。</p>"
	if m.sender != nil {
		mailCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		_ = m.sender.Send(mailCtx, confirmation)
	}
	return result, nil
}

func (m *EmailManager) Status(clientID string) map[string]any {
	status := map[string]any{"client_id": strings.TrimSpace(clientID), "email_bound": false, "binding_limit_per_email": emailBindingLimit}
	if m == nil {
		return status
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.state.Bindings[strings.TrimSpace(clientID)]
	if !ok {
		return status
	}
	email, err := m.decryptEmail(binding.EmailEncrypted)
	if err == nil {
		status["email_masked"] = maskEmail(email)
	}
	status["email_bound"] = true
	status["verified_at"] = binding.VerifiedAt
	return status
}
