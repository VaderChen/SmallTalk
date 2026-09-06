package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const collaborationFileLimit = 8 << 20
const collaborationQuota = 256 << 20
const collaborationLockTTL = 10 * time.Minute

type CollaborationVersion struct {
	Revision     int       `json:"revision"`
	BaseRevision int       `json:"base_revision"`
	SHA256       string    `json:"sha256"`
	Size         int       `json:"size"`
	Author       string    `json:"author_id"`
	CreatedAt    time.Time `json:"created_at"`
	RequestID    string    `json:"request_id"`
}
type CollaborationLock struct {
	Owner     string    `json:"owner_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}
type CollaborationFile struct {
	Versions []CollaborationVersion `json:"versions"`
	Lock     *CollaborationLock     `json:"lock,omitempty"`
}
type CollaborationSandbox struct {
	Ready bool                         `json:"ready"`
	Files map[string]CollaborationFile `json:"files"`
}

func (t *socialTx) collaborationEnabled() (bool, error) {
	if t.db == nil {
		return t.disk.CollaborationEnabled, nil
	}
	var enabled bool
	err := t.db.QueryRow(`SELECT enabled FROM collaboration_settings WHERE id=1`).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return enabled, err
}
func (s *Store) CollaborationEnabled() (bool, error) {
	var enabled bool
	err := s.socialTransaction(false, func(tx *socialTx) error { var e error; enabled, e = tx.collaborationEnabled(); return e })
	return enabled, err
}
func (s *Store) SetCollaborationEnabled(enabled bool) error {
	return s.socialTransaction(true, func(tx *socialTx) error {
		if enabled && s.collaborationDir == "" {
			return fmt.Errorf("SANDBOX目錄尚未設定")
		}
		if tx.db == nil {
			tx.disk.CollaborationEnabled = enabled
			tx.dirty = true
			return nil
		}
		_, err := tx.db.Exec(`INSERT INTO collaboration_settings(id,enabled) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET enabled=EXCLUDED.enabled`, enabled)
		return err
	})
}
func (s *Store) ConfigureCollaboration(directory, publicDirectory string) error {
	// 與聊天室匯出相同的非公開目錄驗證，避免建立第二套路徑判斷。
	probe := &Store{}
	if err := probe.ConfigureChatArchive(directory, publicDirectory); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collaborationDir = probe.chatArchiveDir
	return nil
}
func collaborationPath(value string) (string, error) {
	if value == "" || len(value) > 240 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\:") || path.IsAbs(value) || path.Clean(value) != value || value == "." || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("請使用合法相對路徑，例如src/api/main.go；不可包含..、反斜線或控制字元")
	}
	parts := strings.Split(value, "/")
	if len(parts) > 16 {
		return "", fmt.Errorf("目錄最多16層")
	}
	for _, p := range parts {
		if p == "." || p == ".." || p == "" || strings.TrimSpace(p) != p {
			return "", fmt.Errorf("檔案路徑無效")
		}
	}
	return value, nil
}
func (s *Store) collaborationRoom(tx *socialTx, client, id string, write bool) (AgentChatroom, error) {
	room, err := tx.chatRoom(id)
	if err != nil {
		return room, err
	}
	if !room.Collaboration || room.Sandbox == nil {
		return room, fmt.Errorf("協作空間不存在")
	}
	if err := s.chatAccessLocked(room, client, write); err != nil {
		return room, err
	}
	if !room.Sandbox.Ready && client != room.Owner {
		return room, ErrForbidden
	}
	return room, nil
}
func collaborationCurrent(file CollaborationFile) int {
	if len(file.Versions) == 0 {
		return 0
	}
	return file.Versions[len(file.Versions)-1].Revision
}
func collaborationLockActive(room AgentChatroom, lock *CollaborationLock, now time.Time) bool {
	return lock != nil && lock.ExpiresAt.After(now) && room.Members[lock.Owner] == "joined"
}
func (s *Store) CollaborationLock(client, id, filename, action, token string) (map[string]any, error) {
	filename, err := collaborationPath(filename)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = s.socialTransaction(true, func(tx *socialTx) error {
		room, e := s.collaborationRoom(tx, client, id, true)
		if e != nil {
			return e
		}
		now := time.Now()
		for p, f := range room.Sandbox.Files {
			if len(f.Versions) == 0 && !collaborationLockActive(room, f.Lock, now) {
				delete(room.Sandbox.Files, p)
			}
		}
		file := room.Sandbox.Files[filename]
		active := collaborationLockActive(room, file.Lock, now)
		switch action {
		case "lock":
			if active {
				return fmt.Errorf("檔案已有編輯鎖，請稍後再試")
			}
			if len(room.Sandbox.Files) >= 1000 && len(file.Versions) == 0 && file.Lock == nil {
				return fmt.Errorf("每個SANDBOX最多1000個檔案路徑")
			}
			raw := make([]byte, 24)
			if _, e := rand.Read(raw); e != nil {
				return e
			}
			file.Lock = &CollaborationLock{Owner: client, Token: hex.EncodeToString(raw), ExpiresAt: now.Add(collaborationLockTTL)}
		case "renew", "unlock":
			if !active || file.Lock.Owner != client || token == "" || file.Lock.Token != token {
				return fmt.Errorf("編輯鎖已失效或不屬於此請求")
			}
			if action == "unlock" {
				file.Lock = nil
			} else {
				file.Lock.ExpiresAt = now.Add(collaborationLockTTL)
			}
		default:
			return fmt.Errorf("不支援的鎖定操作")
		}
		if len(file.Versions) == 0 && file.Lock == nil {
			delete(room.Sandbox.Files, filename)
		} else {
			room.Sandbox.Files[filename] = file
		}
		if e := tx.saveChat(room); e != nil {
			return e
		}
		result = map[string]any{"path": filename, "base_revision": collaborationCurrent(file)}
		if file.Lock != nil {
			result["lock_token"] = file.Lock.Token
			result["expires_at"] = file.Lock.ExpiresAt
		}
		return nil
	})
	return result, err
}
func (s *Store) writeCollaborationBlob(room, hash string, data []byte) error {
	root, err := os.OpenRoot(s.collaborationDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.Mkdir(room, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	folder, err := root.OpenRoot(room)
	if err != nil {
		return err
	}
	defer folder.Close()
	// 不可變內容定址檔案；失敗的metadata交易最多留下不被引用的檔案。
	if f, e := folder.Open(hash); e == nil {
		old, e := io.ReadAll(io.LimitReader(f, collaborationFileLimit+1))
		f.Close()
		sum := sha256.Sum256(old)
		if e != nil || hex.EncodeToString(sum[:]) != hash {
			return fmt.Errorf("SANDBOX既有內容雜湊不符")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	nonce := make([]byte, 12)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	temp := ".upload-" + hex.EncodeToString(nonce)
	f, err := folder.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer folder.Remove(temp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = folder.Rename(temp, hash); err != nil {
		return err
	}
	dir, err := folder.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) CommitCollaborationFile(client, id, filename, token, request, encoded string, base int) (map[string]any, error) {
	filename, err := collaborationPath(filename)
	if err != nil {
		return nil, err
	}
	if !chatRequestID(request) || base < 0 || len(encoded) > base64.StdEncoding.EncodedLen(collaborationFileLimit) {
		return nil, fmt.Errorf("request_id/base_revision或檔案大小無效")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) > collaborationFileLimit {
		return nil, fmt.Errorf("請提供最多8MiB檔案的base64")
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	var result map[string]any
	err = s.socialTransaction(true, func(tx *socialTx) error {
		room, e := s.collaborationRoom(tx, client, id, true)
		if e != nil {
			return e
		}
		total := 0
		for p, file := range room.Sandbox.Files {
			for _, version := range file.Versions {
				total += version.Size
				if version.Author == client && version.RequestID == request {
					if p != filename || version.SHA256 != digest || version.BaseRevision != base {
						return fmt.Errorf("request_id已用於不同提交")
					}
					result = map[string]any{"path": filename, "version": version, "duplicate": true}
					return nil
				}
			}
		}
		if total+len(data) > collaborationQuota {
			return fmt.Errorf("SANDBOX版本總容量上限256MiB")
		}
		file := room.Sandbox.Files[filename]
		if !collaborationLockActive(room, file.Lock, time.Now()) || file.Lock.Owner != client || token == "" || file.Lock.Token != token {
			return fmt.Errorf("提交前必須持有有效編輯鎖")
		}
		if collaborationCurrent(file) != base {
			return fmt.Errorf("版本衝突，請重新讀取最新版本")
		}
		if len(file.Versions) >= 1000 {
			return fmt.Errorf("單檔最多1000個版本")
		}
		for p, f := range room.Sandbox.Files {
			if len(f.Versions) > 0 && (strings.HasPrefix(p, filename+"/") || strings.HasPrefix(filename, p+"/")) {
				return fmt.Errorf("檔案與資料夾路徑衝突")
			}
		}
		if e = s.writeCollaborationBlob(id, digest, data); e != nil {
			return e
		}
		version := CollaborationVersion{Revision: base + 1, BaseRevision: base, SHA256: digest, Size: len(data), Author: client, CreatedAt: time.Now(), RequestID: request}
		file.Versions = append(file.Versions, version)
		file.Lock = nil
		room.Sandbox.Files[filename] = file
		event := chatRecord(id, client, s.agentRegistry[client].DisplayName, "file_commit", "", filename, "")
		if e = tx.appendChat(&room, event); e != nil {
			return e
		}
		result = map[string]any{"path": filename, "version": version, "duplicate": false}
		return nil
	})
	return result, err
}
func (s *Store) ReadCollaborationFiles(client, id, filename, action string, revision, offset, limit int) (map[string]any, error) {
	if revision < 0 || offset < 0 || limit < 0 || limit > 65536 {
		return nil, fmt.Errorf("版本/offset/limit無效")
	}
	if limit == 0 {
		limit = 65536
	}
	if filename != "" {
		var e error
		filename, e = collaborationPath(filename)
		if e != nil {
			return nil, e
		}
	}
	var result map[string]any
	err := s.socialTransaction(false, func(tx *socialTx) error {
		room, e := s.collaborationRoom(tx, client, id, false)
		if e != nil {
			return e
		}
		if action == "files" {
			paths := []string{}
			for p, f := range room.Sandbox.Files {
				if len(f.Versions) > 0 {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			out := []map[string]any{}
			for _, p := range paths {
				f := room.Sandbox.Files[p]
				v := f.Versions[len(f.Versions)-1]
				out = append(out, map[string]any{"path": p, "revision": v.Revision, "size": v.Size, "sha256": v.SHA256, "locked": collaborationLockActive(room, f.Lock, time.Now())})
			}
			result = map[string]any{"files": out, "ready": room.Sandbox.Ready}
			return nil
		}
		file := room.Sandbox.Files[filename]
		if len(file.Versions) == 0 {
			return fmt.Errorf("檔案不存在")
		}
		if action == "history" {
			result = map[string]any{"path": filename, "versions": file.Versions}
			return nil
		}
		if action != "read" {
			return fmt.Errorf("讀取操作無效")
		}
		if revision == 0 {
			revision = collaborationCurrent(file)
		}
		if revision > len(file.Versions) {
			return fmt.Errorf("版本不存在")
		}
		version := file.Versions[revision-1]
		root, e := os.OpenRoot(s.collaborationDir)
		if e != nil {
			return e
		}
		defer root.Close()
		f, e := root.Open(filepath.Join(id, version.SHA256))
		if e != nil {
			return e
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, collaborationFileLimit+1))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		if len(data) != version.Size || hex.EncodeToString(sum[:]) != version.SHA256 {
			return fmt.Errorf("檔案完整性檢查失敗")
		}
		if offset > len(data) {
			return fmt.Errorf("offset超過檔案長度")
		}
		end := offset + limit
		if end > len(data) {
			end = len(data)
		}
		result = map[string]any{"path": filename, "revision": revision, "sha256": version.SHA256, "size": version.Size, "content_base64": base64.StdEncoding.EncodeToString(data[offset:end]), "next_offset": end, "has_more": end < len(data)}
		return nil
	})
	return result, err
}

// 僅由後台受保護端點呼叫；Public API只讀功能開關。
func collaborationSettingsJSON(enabled bool) []byte {
	raw, _ := json.Marshal(map[string]any{"enabled": enabled})
	return raw
}
