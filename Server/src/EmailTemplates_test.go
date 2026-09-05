package main

import (
	"context"
	"strings"
	"testing"
)

func TestRegistrationEmailTemplatesAreSeparate(t *testing.T) {
	for _, mode := range []string{registrationModeStandard, registrationModeStrict} {
		t.Run(mode, func(t *testing.T) {
			_, manager, sender := standardTestManager(t)
			if err := manager.ConfigureRegistration(mode, 50); err != nil {
				t.Fatal(err)
			}
			receipt, err := manager.RequestRegistration(context.Background(), "template-agent", "<測試 & 名稱>", "", "template@example.test", "")
			if err != nil {
				t.Fatal(err)
			}
			message := sender.last(t)
			if mode == registrationModeStandard {
				if message.Subject != "瘋之塔｜帳號建立成功" {
					t.Fatal("標準模式主旨錯誤")
				}
				for _, forbidden := range []string{"人工驗證碼", "人工驗證頁", "verify-email.html", "新帳號註冊驗證", receipt.AuthToken} {
					if forbidden != "" && strings.Contains(message.Text+message.HTML, forbidden) {
						t.Fatal("標準信件混入嚴格流程或 TOKEN")
					}
				}
				if strings.Count(message.HTML, "<a ") != 1 || !strings.Contains(message.HTML, "確認備援 Email") || !strings.Contains(message.HTML, "&lt;測試 &amp; 名稱&gt;") {
					t.Fatal("標準信件連結或 HTML 跳脫錯誤")
				}
				result, err := manager.CompleteURL(context.Background(), agentVerificationURLFromMessage(t, message), "")
				if err != nil || result.AuthToken != "" || manager.Status(receipt.ClientID)["email_bound"] != true {
					t.Fatal("備援 Email 確認失敗或重複核發 TOKEN")
				}
			} else {
				if !strings.Contains(message.Subject, "註冊驗證") || !strings.Contains(message.HTML, "人工驗證頁") || strings.Contains(message.HTML, "帳號已建立") {
					t.Fatal("嚴格模式混入標準成功通知")
				}
				id, link, code := emailProofFromMessage(t, message)
				result, err := manager.Complete(context.Background(), id, link, code, "")
				if err != nil || !result.TokenReleased {
					t.Fatal("嚴格人工驗證流程失效")
				}
			}
		})
	}
}
