package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/rs/xid"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const chatArchiveFilename = "transcript.jsonl"

// 路徑只由部署設定提供；MCP 不接受任意檔名或檔案位置。
func (s *Store) ConfigureChatArchive(directory, publicDirectory string) error {
	dir, err := filepath.Abs(directory)
	if err != nil || strings.TrimSpace(directory) == "" {
		return fmt.Errorf("請設定非公開的聊天室匯出目錄")
	}
	public, err := filepath.Abs(publicDirectory)
	if err != nil {
		return err
	}
	if p, e := filepath.EvalSymlinks(public); e == nil {
		public = p
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(public, dir)
	if err != nil {
		return err
	}
	if relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..") {
		return fmt.Errorf("聊天室匯出目錄不可位於公開網站目錄")
	}
	s.chatArchiveMu.Lock()
	defer s.chatArchiveMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatArchiveDir = dir
	return nil
}
func (s *Store) ExportChatroom(id string) error {
	if _, err := xid.FromString(id); err != nil {
		return fmt.Errorf("聊天室ID無效")
	}
	s.chatArchiveMu.Lock()
	defer s.chatArchiveMu.Unlock()
	return s.socialTransaction(true, func(tx *socialTx) error {
		room, err := tx.rawChatRoom(id)
		if err != nil {
			return err
		}
		if s.chatArchiveDir == "" {
			return fmt.Errorf("聊天室匯出尚未設定")
		}
		root, err := os.OpenRoot(s.chatArchiveDir)
		if err != nil {
			return err
		}
		defer root.Close()
		if err := root.MkdirAll(id, 0700); err != nil {
			return err
		}
		info, err := root.Lstat(id)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("聊天室匯出目錄無效")
		}
		tmp := filepath.Join(id, ".transcript-"+xid.New().String()+".tmp")
		f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		defer root.Remove(tmp)
		hash := sha256.New()
		enc := json.NewEncoder(io.MultiWriter(f, hash))
		if err := enc.Encode(map[string]any{"type": "chatroom", "room_id": id, "name": room.Name, "owner_id": room.Owner, "owner_name_at_creation": room.OwnerName, "status": room.Status, "created_at": room.CreatedAt, "closed_at": room.ClosedAt, "through_seq": room.LastSeq}); err != nil {
			return err
		}
		var after, count int64
		for {
			rows, err := tx.chatRecords(id, after, 1000)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				if err := enc.Encode(r); err != nil {
					return err
				}
				after = r.Seq
				count++
			}
		}
		if after != room.LastSeq || count != room.LastSeq {
			return fmt.Errorf("聊天室紀錄序號不完整，停止匯出")
		}
		if err := f.Sync(); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := root.Rename(tmp, filepath.Join(id, chatArchiveFilename)); err != nil {
			return err
		}
		room.Archive = ChatArchive{Directory: filepath.Join(s.chatArchiveDir, id), Filename: chatArchiveFilename, SHA256: hex.EncodeToString(hash.Sum(nil)), ThroughSeq: after, Records: count, ExportedAt: time.Now()}
		return tx.saveChat(room)
	})
}

// metadata 提交失敗時匯出仍為 pending，下次排程重新產生；不回退或刪除 DB 原文。
func (s *Store) ExportPendingChatrooms() error {
	ids := []string{}
	if err := s.socialTransaction(false, func(tx *socialTx) error {
		rooms, err := tx.chatRooms("")
		if err != nil {
			return err
		}
		for _, r := range rooms {
			missing := false
			if _, err := xid.FromString(r.ID); err == nil && s.chatArchiveDir != "" {
				_, err := os.Stat(filepath.Join(s.chatArchiveDir, r.ID, chatArchiveFilename))
				missing = os.IsNotExist(err)
			}
			if r.Archive.ThroughSeq < r.LastSeq || r.Archive.ExportedAt.IsZero() || missing || r.Archive.Directory != filepath.Join(s.chatArchiveDir, r.ID) {
				ids = append(ids, r.ID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var failures int
	for _, id := range ids {
		if err := s.ExportChatroom(id); err != nil {
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d間聊天室匯出失敗，保留待重試狀態", failures)
	}
	return nil
}
func StartChatArchiveWorker(s *Store, interval time.Duration, report func(error)) func() {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		run := func() {
			if err := s.ExportPendingChatrooms(); err != nil && report != nil {
				report(err)
			}
		}
		run()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				run()
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }); <-done }
}
func (s *Store) ReadChatArchive(client, id string, offset int64, maxBytes int) (map[string]any, error) {
	if offset < 0 || maxBytes < 0 || maxBytes > 65536 {
		return nil, fmt.Errorf("offset不可為負數，max_bytes須為0至65536（0只讀中繼資料）")
	}
	s.chatArchiveMu.Lock()
	defer s.chatArchiveMu.Unlock()
	var result map[string]any
	err := s.socialTransaction(false, func(tx *socialTx) error {
		if _, err := s.socialAccountLocked(client, false); err != nil {
			return err
		}
		r, err := tx.chatRoom(id)
		if err != nil {
			return err
		}
		if r.Owner != client {
			return ErrForbidden
		}
		result = chatView(r, client)
		result["archive"] = r.Archive
		result["archive_pending"] = r.LastSeq > r.Archive.ThroughSeq
		if maxBytes == 0 {
			return nil
		}
		if r.Archive.ExportedAt.IsZero() {
			return fmt.Errorf("尚未完成第一次匯出")
		}
		if _, err := xid.FromString(id); err != nil {
			return ErrForbidden
		}
		if s.chatArchiveDir == "" || r.Archive.Directory != filepath.Join(s.chatArchiveDir, id) || r.Archive.Filename != chatArchiveFilename {
			return fmt.Errorf("匯出位置與目前設定不符")
		}
		root, err := os.OpenRoot(s.chatArchiveDir)
		if err != nil {
			return err
		}
		defer root.Close()
		f, err := root.Open(filepath.Join(id, chatArchiveFilename))
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		size, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != r.Archive.SHA256 {
			return fmt.Errorf("匯出檔與資料庫雜湊不符，停止調閱")
		}
		if offset > size {
			return fmt.Errorf("offset超過檔案長度")
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
		if err != nil {
			return err
		}
		result["content_base64"] = base64.StdEncoding.EncodeToString(data)
		result["offset"] = offset
		result["next_offset"] = offset + int64(len(data))
		result["size_bytes"] = size
		result["has_more"] = offset+int64(len(data)) < size
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
