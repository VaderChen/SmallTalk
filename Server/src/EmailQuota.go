package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var errDailyEmailLimit = errors.New("daily_email_limit_reached")

// 站台端保守計算每次對寄信供應商的請求，包含失敗與重試。
// 不以送達與否回補額度，避免供應商已收件但回應遺失造成超額。
func (m *EmailManager) ConfigureEmailLimit(limit int) error {
	if limit < 0 {
		return fmt.Errorf("email_daily_send_limit must be non-negative")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.DailyEmailLimit != nil && *m.state.DailyEmailLimit < 0 {
		return fmt.Errorf("saved daily email limit is invalid")
	}
	m.dailyEmailLimit = limit
	return nil
}

func (m *EmailManager) emailLimitLocked() int {
	if m.state.DailyEmailLimit != nil {
		return *m.state.DailyEmailLimit
	}
	return m.dailyEmailLimit
}

func (m *EmailManager) EmailDeliverySettings() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	used := m.state.EmailAttempts
	if m.state.EmailQuotaDay != now.Format("2006-01-02") {
		used = 0
	}
	limit := m.emailLimitLocked()
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	y, month, d := now.Date()
	return map[string]any{"daily_send_limit": limit, "used_today": used, "remaining_today": remaining,
		"quota_day": now.Format("2006-01-02"), "resets_at": time.Date(y, month, d+1, 0, 0, 0, 0, now.Location()).Format(time.RFC3339),
		"counting_rule": "每次實際呼叫寄信服務計一次，包含驗證、綁定、復原、完成通知、失敗與重試；不代表已送達封數。"}
}

func (m *EmailManager) UpdateEmailLimit(limit int) error {
	if limit < 0 {
		return fmt.Errorf("每日寄信上限不可小於 0；0 代表停止寄信")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.state.DailyEmailLimit
	m.state.DailyEmailLimit = &limit
	if err := m.saveLocked(); err != nil {
		m.state.DailyEmailLimit = previous
		return err
	}
	return nil
}

func (m *EmailManager) sendEmail(ctx context.Context, message EmailMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.Available() {
		return fmt.Errorf("email delivery is not configured")
	}
	m.mu.Lock()
	day := m.now().Format("2006-01-02")
	previousDay, previousUsed := m.state.EmailQuotaDay, m.state.EmailAttempts
	used := previousUsed
	if previousDay != day {
		used = 0
	}
	if used >= m.emailLimitLocked() {
		m.mu.Unlock()
		return fmt.Errorf("%w: 今日站台寄信額度已用完或寄信已停用，請於次日或管理員調整後再試", errDailyEmailLimit)
	}
	m.state.EmailQuotaDay, m.state.EmailAttempts = day, used+1
	if err := m.saveLocked(); err != nil {
		m.state.EmailQuotaDay, m.state.EmailAttempts = previousDay, previousUsed
		m.mu.Unlock()
		return fmt.Errorf("email quota persistence failed; email not sent")
	}
	m.mu.Unlock()
	return m.sender.Send(ctx, message)
}
