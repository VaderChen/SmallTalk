package main

import (
	"database/sql"
	"fmt"
)

func (pg *PostgresStore) initProfileSchema() error {
	tx, err := pg.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE agent_registry ADD COLUMN IF NOT EXISTS display_name_key TEXT;
 ALTER TABLE agent_registry ADD COLUMN IF NOT EXISTS renamed_at TEXT;
 ALTER TABLE agent_registry ADD COLUMN IF NOT EXISTS name_history JSONB;`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT client_id,display_name,display_name_key FROM agent_registry`)
	if err != nil {
		return err
	}
	type update struct{ id, key string }
	updates := []update{}
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var name, key sql.NullString
		if err := rows.Scan(&id, &name, &key); err != nil {
			rows.Close()
			return err
		}
		normalized := profileNameKey(name.String)
		if normalized != "" && seen[normalized] {
			rows.Close()
			return fmt.Errorf("既有帳號存在重複名稱，請先由管理員處理，未變更名稱資料")
		}
		seen[normalized] = true
		if !key.Valid || key.String != normalized {
			updates = append(updates, update{id, normalized})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, u := range updates {
		if _, err := tx.Exec(`UPDATE agent_registry SET display_name_key=$2 WHERE client_id=$1`, u.id, u.key); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS agent_registry_display_name_key_unique ON agent_registry(display_name_key) WHERE display_name_key <> ''`); err != nil {
		return err
	}
	return tx.Commit()
}

// 舊名稱衝突或結構升級失敗不能改連其他資料庫／退回舊檔案。
type ProfileSchemaError struct{ Err error }

func (e *ProfileSchemaError) Error() string {
	return fmt.Sprintf("帳號名稱資料升級失敗: %v", e.Err)
}
func (e *ProfileSchemaError) Unwrap() error { return e.Err }
