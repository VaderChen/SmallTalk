package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func standardTestManager(t *testing.T) (*Store, *EmailManager, *memoryEmailSender) {
	t.Helper()
	initPersistentJWSKeys(t.TempDir())
	store := NewStore(t.TempDir(), 20, false)
	sender := &memoryEmailSender{}
	manager := newTestEmailManager(t, store, sender)
	if err := manager.ConfigureRegistration(registrationModeStandard, 50); err != nil {
		t.Fatal(err)
	}
	return store, manager, sender
}

func TestStandardRegistrationAndVerifiedRecovery(t *testing.T) {
	store, manager, sender := standardTestManager(t)
	ctx := context.Background()
	receipt, err := manager.RequestRegistration(ctx, "standard-agent", "標準帳號", "", "owner@example.test", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "registered" || receipt.AuthToken == "" || !receipt.EmailSent || receipt.TokenFingerprint != sha256Hex(receipt.AuthToken)[:12] {
		t.Fatal("標準模式未立即回傳有效 TOKEN 與指紋")
	}
	if _, ok := store.GetAuthTokenRecord(receipt.AuthToken); !ok {
		t.Fatal("TOKEN 未保存")
	}
	mail := sender.last(t)
	if strings.Contains(mail.Text+mail.HTML, receipt.AuthToken) || !strings.Contains(mail.Text, receipt.TokenFingerprint) {
		t.Fatal("信件憑證內容錯誤")
	}
	if manager.Status(receipt.ClientID)["email_bound"] != false {
		t.Fatal("未驗證 Email 卻已綁定")
	}
	if _, exists := manager.state.Bindings[receipt.ClientID]; exists {
		t.Fatal("未確認資料不可寫入舊版信任的 Bindings")
	}
	before := len(sender.messages)
	manager.RequestRecovery(ctx, receipt.ClientID, "owner@example.test", "192.0.2.2")
	if len(sender.messages) != before {
		t.Fatal("未驗證 Email 竟可觸發復原")
	}
	// 標準註冊確認與既有帳號綁定共用同帳號／Email 的防重寄窗口。
	duplicate, err := manager.RequestBinding(ctx, receipt.ClientID, "owner@example.test", "192.0.2.1")
	if err != nil || duplicate.EmailSent || len(sender.messages) != before {
		t.Fatal("綁定繞過註冊信的 24 小時防重寄")
	}
	if err := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: registrationModeStrict, DailyLimit: 50}); err != nil {
		t.Fatal(err)
	}
	confirmed, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, mail), "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.TokenReleased || confirmed.AuthToken != "" || manager.Status(receipt.ClientID)["email_bound"] != true {
		t.Fatal("標準確認不可重新核發 TOKEN")
	}
	if entry, _ := store.GetAgentRegistry(receipt.ClientID); entry.Token != receipt.AuthToken {
		t.Fatal("切換模式或確認 Email 變更了 TOKEN")
	}
	if _, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, mail), ""); err == nil {
		t.Fatal("確認連結可重複使用")
	}
	manager.RequestRecovery(ctx, receipt.ClientID, "owner@example.test", "192.0.2.2")
	recoveryMail := sender.last(t)
	challengeID, _, _ := emailProofFromMessage(t, recoveryMail)
	challenge := manager.state.Challenges[challengeID]
	created, _ := time.Parse(time.RFC3339Nano, challenge.CreatedAt)
	expires, _ := time.Parse(time.RFC3339Nano, challenge.ExpiresAt)
	if expires.Sub(created) != 30*time.Minute {
		t.Fatal("復原期限不是 30 分鐘")
	}
	recovered, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, recoveryMail), "192.0.2.2")
	if err != nil || !recovered.TokenRotated || recovered.AuthToken == receipt.AuthToken {
		t.Fatal("復原未換發 TOKEN")
	}
	if _, ok := store.GetAuthTokenRecord(receipt.AuthToken); ok {
		t.Fatal("復原後舊 TOKEN 仍可用")
	}
}

type retryTestSender struct {
	calls     int
	failCount int
	keys      []string
}

func (s *retryTestSender) Send(_ context.Context, message EmailMessage) error {
	s.calls++
	s.keys = append(s.keys, message.IdempotencyKey)
	if s.calls <= s.failCount {
		return fmt.Errorf("模擬寄信失敗")
	}
	return nil
}

func TestStandardDeliveryFailureKeepsAccountAndLimits(t *testing.T) {
	for _, failures := range []int{1, 2} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			store, manager, _ := standardTestManager(t)
			sender := &retryTestSender{failCount: failures}
			manager.sender = sender
			if err := manager.SetDailyRegistrationLimit(1); err != nil {
				t.Fatal(err)
			}
			receipt, err := manager.RequestRegistration(context.Background(), "mail-fail", "寄信測試", "", "mail@example.test", "")
			if err != nil || receipt.AuthToken == "" {
				t.Fatal("寄信失敗不應報註冊失敗")
			}
			if sender.calls != 2 || sender.keys[0] == "" || sender.keys[0] != sender.keys[1] {
				t.Fatal("重試次数或冪等鍵錯誤")
			}
			if receipt.EmailSent != (failures == 1) {
				t.Fatal("寄信狀態錯誤")
			}
			if failures == 2 && receipt.Status != "registered_email_delivery_failed" {
				t.Fatal("未說明寄信失敗")
			}
			if _, ok := store.GetAuthTokenRecord(receipt.AuthToken); !ok {
				t.Fatal("寄信失敗撤銷了 TOKEN")
			}
			limited, err := manager.RequestRegistration(context.Background(), "second", "另一帳號", "", "second@example.test", "")
			if err != nil || limited.Status != "daily_registration_limit_reached" || sender.calls != 2 {
				t.Fatal("寄信失敗繞過每日申請額度")
			}
		})
	}
}

func TestRegistrationModesPendingSwitchAndEmailCorrection(t *testing.T) {
	_, manager, sender := standardTestManager(t)
	ctx := context.Background()
	if err := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: registrationModeStrict, DailyLimit: 50}); err != nil {
		t.Fatal(err)
	}
	_, err := manager.RequestRegistration(ctx, "strict-pending", "嚴格待確認", "", "strict@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	strictMail := sender.last(t)
	// 舊狀態檔中的 challenge 無 mode，仍須沿用嚴格流程。
	for id, challenge := range manager.state.Challenges {
		challenge.RegistrationMode = ""
		manager.state.Challenges[id] = challenge
	}
	if err := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: registrationModeStandard, DailyLimit: 50}); err != nil {
		t.Fatal(err)
	}
	strictResult, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, strictMail), "")
	if err != nil || !strictResult.TokenReleased {
		t.Fatal("模式切換破壞既有嚴格申請")
	}
	receipt, err := manager.RequestRegistration(ctx, "email-correction", "改信箱測試", "", "typo@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	oldMail := sender.last(t)
	if _, err := manager.RequestBinding(ctx, receipt.ClientID, "correct@example.test", ""); err != nil {
		t.Fatal(err)
	}
	newMail := sender.last(t)
	if _, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, oldMail), ""); err == nil {
		t.Fatal("改綁後舊連結仍可使用")
	}
	if _, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, newMail), ""); err != nil {
		t.Fatal(err)
	}
}

func TestStandardEmailAccountLimitAndConcurrentDuplicate(t *testing.T) {
	_, manager, sender := standardTestManager(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := manager.RequestRegistration(ctx, fmt.Sprintf("limit-%d", i), fmt.Sprintf("上限測試 %d", i), "", "shared@example.test", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.RequestRegistration(ctx, "limit-six", "第六個", "", "shared@example.test", ""); err == nil {
		t.Fatal("允許第六個帳號")
	}
	if len(sender.messages) != 5 {
		t.Fatal("額滿仍寄信")
	}
	var wg sync.WaitGroup
	receipts := make(chan EmailChallengeReceipt, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := manager.RequestRegistration(ctx, "concurrent", "同時申請", "", "parallel@example.test", "")
			receipts <- r
		}()
	}
	wg.Wait()
	close(receipts)
	issued := 0
	for r := range receipts {
		if r.AuthToken != "" {
			issued++
		}
	}
	if issued != 1 || len(sender.messages) != 6 {
		t.Fatal("並發重複核發或寄信")
	}
}

func TestRegistrationSettingsPersistenceAndRootOnly(t *testing.T) {
	store, manager, _ := standardTestManager(t)
	api := &PermissionsAPI{Store: store, Email: manager}
	body := `{"registration_mode":"strict","daily_registration_limit":7}`
	request := httptest.NewRequest(http.MethodPost, "http://example.test/permissions/registration-settings", strings.NewReader(body))
	if result := string(api.Process(httptest.NewRecorder(), request, nil, nil, nil, body)); !strings.Contains(result, "unauthorized") {
		t.Fatal("訪客可修改註冊政策")
	}
	now := time.Now()
	if _, err := store.UpsertAuthToken(AuthTokenRecord{Token: "local-root-test", ClientID: "root", Kind: "session-human", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}, false); err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer local-root-test")
	var settings EmailRegistrationSettings
	if err := json.Unmarshal(api.Process(httptest.NewRecorder(), request, nil, nil, nil, body), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "strict" || settings.DailyLimit != 7 {
		t.Fatal("管理設定未生效")
	}
	reloaded, err := NewEmailManager(store, filepath.Dir(manager.statePath), manager.publicBaseURL, manager.encryptionKey, manager.pepper, manager.sender)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.ConfigureRegistration("standard", 50); err != nil {
		t.Fatal(err)
	}
	if reloaded.RegistrationSettings() != settings {
		t.Fatal("重啟未保存管理設定優先權")
	}
	for _, invalid := range []EmailRegistrationSettings{{Mode: "typo", DailyLimit: 7}, {Mode: "standard", DailyLimit: 0}} {
		if err := reloaded.UpdateRegistrationSettings(invalid); err == nil {
			t.Fatal("接受無效設定")
		}
	}
	if reloaded.RegistrationSettings() != settings {
		t.Fatal("無效設定污染現有政策")
	}
	fresh, err := NewEmailManager(store, t.TempDir(), manager.publicBaseURL, manager.encryptionKey, manager.pepper, manager.sender)
	if err != nil || fresh.RegistrationSettings().Mode != "standard" {
		t.Fatal("全新預設不是標準模式")
	}
}

func TestStandardConfirmationStorageFailureIsFailClosed(t *testing.T) {
	_, manager, sender := standardTestManager(t)
	ctx := context.Background()
	receipt, err := manager.RequestRegistration(ctx, "storage-test", "保存失敗測試", "", "storage@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	verificationURL := agentVerificationURLFromMessage(t, sender.last(t))
	originalPath := manager.statePath
	manager.statePath = t.TempDir() // 刻意令原子 rename 失敗，不能在記憶體提前標記驗證完成。
	if _, err := manager.CompleteURL(ctx, verificationURL, ""); err == nil {
		t.Fatal("預期狀態保存失敗")
	}
	if manager.Status(receipt.ClientID)["email_bound"] != false {
		t.Fatal("保存失敗卻允許復原")
	}
	manager.statePath = originalPath
	if _, err := manager.CompleteURL(ctx, verificationURL, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryExpiresAt30MinutesWithoutRevokingToken(t *testing.T) {
	store, manager, sender := standardTestManager(t)
	ctx := context.Background()
	receipt, err := manager.RequestRegistration(ctx, "expiry-test", "到期測試", "", "expiry@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, sender.last(t)), ""); err != nil {
		t.Fatal(err)
	}
	manager.RequestRecovery(ctx, receipt.ClientID, "expiry@example.test", "")
	mail := sender.last(t)
	id, _, _ := emailProofFromMessage(t, mail)
	expiresAt, _ := time.Parse(time.RFC3339Nano, manager.state.Challenges[id].ExpiresAt)
	manager.now = func() time.Time { return expiresAt }
	if _, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, mail), ""); err == nil {
		t.Fatal("到期邊界仍可復原")
	}
	if _, ok := store.GetAuthTokenRecord(receipt.AuthToken); !ok {
		t.Fatal("失敗復原撤銷了現有 TOKEN")
	}
}

func TestStandardHTTPRegistrationAndReservedIdentities(t *testing.T) {
	store, manager, _ := standardTestManager(t)
	manager.sender = nil
	api := &HttpAPI_auth{Store: store, Email: manager}
	body := `{"client_id":"http-new","display_name":"HTTP 新帳號","mac_address":"AABBCCDDEE11","email":"http@example.test"}`
	req := httptest.NewRequest(http.MethodPost, "http://example.test/auth/dev/register", strings.NewReader(body))
	var result map[string]any
	if err := json.Unmarshal(api.handleDevRegister(req, body, true), &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "registered_email_delivery_failed" || result["token_released"] != true || result["auth_token"] == "" {
		t.Fatal("HTTP 路線未依標準模式處理")
	}
	for _, id := range []string{"root", "SYSTEM", "Guest"} {
		if _, err := manager.RequestRegistration(context.Background(), id, "保留名稱", "", "reserved@example.test", ""); err == nil {
			t.Fatal("可經共用註冊路線建立保留帳號")
		}
	}
}

func TestBindingLinkCannotSurviveTokenRotation(t *testing.T) {
	store, manager, sender := standardTestManager(t)
	ctx := context.Background()
	receipt, err := manager.RequestRegistration(ctx, "rotation-test", "換發後綁定保護", "", "original@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteURL(ctx, agentVerificationURLFromMessage(t, sender.last(t)), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RequestBinding(ctx, receipt.ClientID, "new@example.test", ""); err != nil {
		t.Fatal(err)
	}
	oldBindingURL := agentVerificationURLFromMessage(t, sender.last(t))
	if _, err := rotateApprovedAgentToken(store, receipt.ClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteURL(ctx, oldBindingURL, ""); err == nil {
		t.Fatal("TOKEN 撤銷後旧綁定連結仍可換信箱")
	}
	if manager.state.Bindings[receipt.ClientID].EmailHash != manager.emailHash("original@example.test") {
		t.Fatal("失敗的綁定覆寫了備援信箱")
	}
}

// 本機 HTTP MCP smoke：用模擬郵件測兩模式，完全不呼叫正式站台或 Resend。
func TestRegistrationModesLocalHTTPSmoke(t *testing.T) {
	store, manager, sender := standardTestManager(t)
	if _, err := store.CreateProject("default", "測試"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "smoke", "測試看板", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: store, Email: manager}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func(token string) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "registration-smoke", Version: "1"}, nil)
		httpClient := http.DefaultClient
		if token != "" {
			httpClient = &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: token}}
		}
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	call := func(session *mcp.ClientSession, name string, args map[string]any) map[string]any {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result == nil || result.IsError {
			t.Fatalf("MCP %s 失敗: %v", name, err)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	anonymous := connect("")
	registered := call(anonymous, "smalltalk_request_registration", map[string]any{"display_name": "HTTP 標準測試", "email": "http-standard@example.test"})
	token, _ := registered["auth_token"].(string)
	if token == "" || registered["write_access"] != true || registered["registration_mode"] != "standard" {
		t.Fatal("HTTP 標準註冊未成功")
	}
	authorized := connect(token)
	status := call(authorized, "smalltalk_auth_status", map[string]any{})
	if status["authenticated"] != true || status["client_id"] != registered["client_id"] {
		t.Fatal("新 TOKEN 不能登入")
	}
	call(authorized, "smalltalk_create_article", map[string]any{"project_id": "default", "room_id": "smoke", "title": "本機測試", "text": "本機驗證：非空文章，不上傳至正式站台。"})
	if err := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: "strict", DailyLimit: 7}); err != nil {
		t.Fatal(err)
	}
	policy := call(anonymous, "smalltalk_registration_policy", map[string]any{})
	if policy["registration_mode"] != "strict" || policy["daily_registration_limit"] != float64(7) {
		t.Fatal("同一 MCP session 政策未即時更新")
	}
	strict := call(anonymous, "smalltalk_request_registration", map[string]any{"display_name": "HTTP 嚴格測試", "email": "http-strict@example.test"})
	if strict["token_released"] != false || strict["account_status"] != "not_created" {
		t.Fatal("嚴格模式過早核發")
	}
	verified := call(anonymous, "smalltalk_complete_email_verification", map[string]any{"verification_url": agentVerificationURLFromMessage(t, sender.last(t))})
	if verified["token_released"] != true {
		t.Fatal("嚴格驗證未核發")
	}
	if state := call(authorized, "smalltalk_auth_status", map[string]any{}); state["authenticated"] != true {
		t.Fatal("切換模式使既有 TOKEN 失效")
	}
}
