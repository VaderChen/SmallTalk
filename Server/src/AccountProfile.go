package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type NameChange struct {
	OldName   string `json:"old_name"`
	NewName   string `json:"new_name"`
	ChangedAt string `json:"changed_at"`
}

// 與 Unicode EqualFold 一致，不依賴資料庫 locale；不合併外觀相似但不同的字元。
func profileNameKey(name string) string {
	return strings.Map(func(r rune) rune {
		min := r
		for q := unicode.SimpleFold(r); q != r; q = unicode.SimpleFold(q) {
			if q < min {
				min = q
			}
		}
		return min
	}, strings.TrimSpace(name))
}

func validateProfileName(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return fmt.Errorf("名稱須為 1 至 80 個字元")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return fmt.Errorf("名稱不可含控制字元或隱藏格式字元")
		}
	}
	for _, reserved := range []string{"root", "system", "guest"} {
		if profileNameKey(name) == profileNameKey(reserved) {
			return fmt.Errorf("此名稱為系統保留名稱")
		}
	}
	return nil
}

// 以台北時間計算一個曆月；月底不溢出至再下一個月。
func nextRenameAt(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.Time{}, err
	}
	t = t.In(loc)
	first := time.Date(t.Year(), t.Month()+1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
	lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, loc).Day()
	day := t.Day()
	if day > lastDay {
		day = lastDay
	}
	return first.AddDate(0, 0, day-1), nil
}

func checkRenameCooldown(entry *AgentRegistryEntry, now time.Time) error {
	next, err := nextRenameAt(entry.RenamedAt)
	if err != nil {
		return fmt.Errorf("改名紀錄異常，請聯絡管理員")
	}
	if now.Before(next) {
		return fmt.Errorf("一個月只能改名一次，下次可改名時間：%s", next.Format(time.RFC3339))
	}
	return nil
}

func (s *Store) AccountProfile(clientID string) (map[string]any, error) {
	entry, ok := s.GetAgentRegistry(clientID)
	if !ok || !entry.Approved || entry.Blocked {
		return nil, ErrForbidden
	}
	next, err := nextRenameAt(entry.RenamedAt)
	if err != nil {
		return nil, err
	}
	nextText := ""
	if !next.IsZero() {
		nextText = next.Format(time.RFC3339)
	}
	return map[string]any{"client_id": entry.ClientID, "display_name": entry.DisplayName, "registered_at": entry.RegisteredAt, "renamed_at": entry.RenamedAt, "next_rename_at": nextText, "can_rename": !s.IsAgentReadOnly(clientID) && !time.Now().Before(next), "name_history": entry.NameHistory}, nil
}

// 舊版看板可能用顯示名稱指定版主；改名前正規化為帳號 ID，避免失權或被承接舊名者冒用。
func (s *Store) persistProfileRenameLocked(entry, previous *AgentRegistryEntry) error {
	changes := []boardRoleChange{}
	if other := s.agentRegistry[previous.DisplayName]; other == nil || other.ClientID == entry.ClientID {
		for pid, p := range s.projects {
			for _, r := range p.Rooms {
				if r != nil && strings.EqualFold(strings.TrimSpace(r.Owner), previous.DisplayName) && r.Owner != entry.ClientID {
					changes = append(changes, boardRoleChange{pid, r, r.Owner, entry.ClientID})
				}
			}
		}
	}
	if s.pg != nil {
		tx, err := s.pg.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := saveAgentRegistryEntry(tx, entry); err != nil {
			return err
		}
		for _, c := range changes {
			if _, err := tx.Exec(`UPDATE boards SET owner=$3,updated_at=NOW() WHERE project_id=$1 AND room_id=$2`, c.projectID, c.room.ID, c.owner); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		for _, c := range changes {
			c.room.Owner = c.owner
		}
		return nil
	}
	saved := []boardRoleChange{}
	rollback := func(err error) error {
		for _, c := range saved {
			c.room.Owner = c.previousOwner
			if e := s.saveRoomMetaLocked(c.projectID, c.room); e != nil {
				err = fmt.Errorf("%w; 版主資料回復失敗: %v", err, e)
			}
		}
		return err
	}
	for _, c := range changes {
		c.room.Owner = c.owner
		if err := s.saveRoomMetaLocked(c.projectID, c.room); err != nil {
			c.room.Owner = c.previousOwner
			return rollback(err)
		}
		saved = append(saved, c)
	}
	if err := s.saveRegistryLocked(); err != nil {
		return rollback(err)
	}
	return nil
}
