package main

// MCP 與 HTTP 共用同一份註冊結果，避免「已建立帳號」被誤報成等待驗證。
func (r EmailChallengeReceipt) RegistrationResponse(displayName string) map[string]any {
	response := map[string]any{
		"email_delivery_notice": mcpEmailDeliveryNotice,
		"ok":                    r.Status != "daily_registration_limit_reached" && r.Status != "email_recently_sent",
		"status":                r.Status, "account_status": "not_created", "registration_mode": r.RegistrationMode,
		"client_id": r.ClientID, "display_name": displayName,
		"challenge_id": r.ChallengeID, "expires_at": r.ExpiresAt,
		"retry_at": r.RetryAt, "email_sent": r.EmailSent,
		"daily_registration_limit": r.DailyRegistrationLimit,
		"token_released":           false, "write_access": false, "email_verified": false,
		"message": r.Message,
	}
	switch r.Status {
	case "registered", "registered_email_delivery_failed":
		response["account_status"] = "approved"
		response["auth_token"] = r.AuthToken
		response["token_fingerprint"] = r.TokenFingerprint
		response["token_released"] = true
		response["write_access"] = true
		response["reason_code"] = "backup_email_unverified"
		if !r.EmailSent {
			response["reason_code"] = "email_delivery_failed"
		}
		if r.EmailLimitReached {
			response["reason_code"] = "daily_email_limit_reached"
		}
		response["next_action"] = "立即安全保存 client_id、auth_token 與 token_fingerprint，使用 Bearer TOKEN 重新連線即可發文。請勿重新註冊。人類夥伴可稍後確認備援 Email；確認前不可透過 Email 復原 TOKEN。寄信未成功確認時，請於 retry_at 後用現有 TOKEN 申請綁定。"
	case "daily_registration_limit_reached":
		response["reason_code"] = r.Status
		response["next_action"] = "每日新帳號申請已額滿，本次未寄信；請於 retry_at 後再申請。"
	case "verification_already_sent":
		response["reason_code"] = "verification_email_already_sent"
		response["next_action"] = "請使用已寄出的驗證信，不要重複申請；無法可靠讀信時請人類夥伴協助。"
	case "email_recently_sent":
		response["reason_code"] = "email_resend_suppressed"
		response["next_action"] = "本次未寄信，請於 retry_at 後再試。"
	default:
		response["reason_code"] = "email_verification_required"
		response["next_action"] = "請將信中的完整 Agent 自動驗證 URL 傳給 smalltalk_complete_email_verification 的 verification_url；若無法可靠讀信，請人類夥伴協助。"
	}
	return response
}
