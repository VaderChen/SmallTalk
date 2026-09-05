package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEmailQuotaConcurrentPersistentAndDailyReset(t *testing.T) {
	store, manager, sender := standardTestManager(t)
	if err := manager.UpdateEmailLimit(3); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, 9, 5, 23, 59, 0, 0, time.FixedZone("Taipei", 8*3600))
	manager.now = func() time.Time { return current }
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = manager.sendEmail(context.Background(), EmailMessage{To: "test@example.test"})
		}()
	}
	wg.Wait()
	if len(sender.messages) != 3 || manager.EmailDeliverySettings()["used_today"] != 3 {
		t.Fatal("並發寄送突破上限")
	}
	reloaded, err := NewEmailManager(store, filepath.Dir(manager.statePath), manager.publicBaseURL, manager.encryptionKey, manager.pepper, sender)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.now = manager.now
	if reloaded.EmailDeliverySettings()["remaining_today"] != 0 {
		t.Fatal("重啟重置了額度")
	}
	current = current.Add(2 * time.Minute)
	if err := reloaded.sendEmail(context.Background(), EmailMessage{}); err != nil {
		t.Fatal(err)
	}
	if reloaded.EmailDeliverySettings()["used_today"] != 1 || len(sender.messages) != 4 {
		t.Fatal("次日未重算額度")
	}
}

func TestEmailQuotaFailureRetryAndModes(t *testing.T) {
	store, manager, _ := standardTestManager(t)
	sender := &retryTestSender{failCount: 10}
	manager.sender = sender
	if err := manager.UpdateEmailLimit(1); err != nil {
		t.Fatal(err)
	}
	result, err := manager.RequestRegistration(context.Background(), "quota-standard", "額度測試", "", "quota@example.test", "")
	if err != nil || result.AuthToken == "" || !result.EmailLimitReached {
		t.Fatal("標準模式額满錯誤處理")
	}
	if sender.calls != 1 || manager.EmailDeliverySettings()["used_today"] != 1 {
		t.Fatal("失敗或重試未受額度約束")
	}
	if result.RegistrationResponse("test")["reason_code"] != "daily_email_limit_reached" {
		t.Fatal("MCP 未說明額滿")
	}
	if err := manager.UpdateRegistrationSettings(EmailRegistrationSettings{Mode: "strict", DailyLimit: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RequestRegistration(context.Background(), "quota-strict", "嚴格額度測試", "", "strict@example.test", ""); err == nil {
		t.Fatal("嚴格模式應回報無法寄信")
	}
	if _, exists := store.GetAgentRegistry("quota-strict"); exists {
		t.Fatal("嚴格模式未寄信卻建帳號")
	}
	if err := manager.UpdateEmailLimit(-1); err == nil {
		t.Fatal("接受負上限")
	}
	if err := manager.UpdateEmailLimit(0); err != nil {
		t.Fatal(err)
	}
	if err := manager.sendEmail(context.Background(), EmailMessage{}); err == nil {
		t.Fatal("零上限仍寄信")
	}
}

func TestEmailQuotaIncludesCompletionNotice(t *testing.T) {
	_, manager, sender := standardTestManager(t)
	if err := manager.UpdateEmailLimit(1); err != nil {
		t.Fatal(err)
	}
	_, err := manager.RequestRegistration(context.Background(), "notice-quota", "通知額度測試", "", "notice@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteURL(context.Background(), agentVerificationURLFromMessage(t, sender.last(t)), ""); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 {
		t.Fatal("完成通知突破額度")
	}
	if manager.Status("notice-quota")["email_bound"] != true {
		t.Fatal("完成通知無額度不應撤銷驗證")
	}
}
