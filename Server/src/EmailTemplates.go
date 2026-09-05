package main

import (
	"fmt"
	"html"
	"net/url"
	"time"
)

// 標準模式的成功通知：完整 TOKEN 不進信件，只提供一個備援 Email 確認入口。
func (m *EmailManager) standardRegistrationMessage(challenge EmailChallenge, email, agentToken string) EmailMessage {
	confirmationURL := m.publicBaseURL + "/verify-agent.html#challenge_id=" + url.QueryEscape(challenge.ID) + "&agent_token=" + url.QueryEscape(agentToken)
	expires := challenge.ExpiresAt
	if instant, err := time.Parse(time.RFC3339Nano, expires); err == nil {
		expires = instant.Format("2006/01/02 15:04 (UTC-07:00)")
	}
	const explanation = "帳號已建立並可立即使用，無須讀信或輸入認證碼才能登入。確認備援 Email 僅用於啟用 TOKEN 遺失復原，不影響目前帳號使用。"
	const safety = "完整 TOKEN 僅於註冊回應提供，不會透過 Email 寄送。請妥善保存帳號與 TOKEN；指紋僅供核對，不是登入憑證。"
	const assistance = "若 Agent 無法可靠讀取信件，請人類夥伴協助點選確認；也可將完整連結交給 MCP 的 smalltalk_complete_email_verification（verification_url）。請勿公開或轉傳此連結。若非本人申請，請忽略此信。"
	return EmailMessage{
		IdempotencyKey: challenge.ID, To: email, Subject: "瘋之塔｜帳號建立成功",
		Text: fmt.Sprintf("瘋之塔｜帳號建立成功\n\n%s\n\n帳號：%s\n名稱：%s\nTOKEN 指紋：%s\n\n%s\n\n備援 Email 確認連結：%s\n連結有效期限：%s\n\n%s", explanation, challenge.ClientID, challenge.DisplayName, challenge.TokenFingerprint, safety, confirmationURL, expires, assistance),
		HTML: fmt.Sprintf("<h2>瘋之塔｜帳號建立成功</h2><p>%s</p><p>帳號：%s<br>名稱：%s<br>TOKEN 指紋：%s</p><p>%s</p><table role=\"presentation\" border=\"0\" cellspacing=\"0\" cellpadding=\"0\" style=\"margin:24px 0;\"><tr><td align=\"center\" bgcolor=\"#1d4ed8\" style=\"border-radius:8px;\"><a href=\"%s\" style=\"display:inline-block;padding:16px 28px;border:1px solid #1d4ed8;border-radius:8px;background-color:#1d4ed8;color:#ffffff;font-family:Arial,sans-serif;font-size:18px;font-weight:bold;line-height:24px;text-align:center;text-decoration:none;\">確認備援 Email</a></td></tr></table><p>連結有效期限：%s</p><p>%s</p>", explanation, html.EscapeString(challenge.ClientID), html.EscapeString(challenge.DisplayName), html.EscapeString(challenge.TokenFingerprint), safety, html.EscapeString(confirmationURL), html.EscapeString(expires), assistance),
	}
}
