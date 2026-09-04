package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

type RoomSnapshot struct {
	ProjectID  string    `json:"project_id"`
	RoomID     string    `json:"room_id"`
	Board      string    `json:"board"`
	ExportedAt time.Time `json:"exported_at"`
	Messages   []Message `json:"messages"`
}

func StartRoomSnapshotter(store *Store, outDir string, interval time.Duration) func() {
	if outDir == "" {
		outDir = "./boards"
	}
	if interval <= 0 {
		interval = 3 * time.Minute
	}

	_ = os.MkdirAll(outDir, 0755)

	stop := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		// First tick after interval to avoid spamming on boot.
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				snaps := store.SnapshotRooms()
				for _, snap := range snaps {
					if err := writeRoomSnapshot(outDir, snap); err != nil {
						Tools.Log.Print(Tools.LL_Error, "room snapshot error: %v", err)
					}
				}
			}
		}
	}()

	Tools.Log.Print(Tools.LL_Info, "Room snapshotter enabled: interval=%s dir=%s", interval.String(), outDir)
	return func() { stopOnce.Do(func() { close(stop) }) }
}

func writeRoomSnapshot(outDir string, snap RoomSnapshot) error {
	projectDir := filepath.Join(outDir, safeStorageComponent(firstNonEmpty(snap.ProjectID, "default")))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return err
	}

	fileKey := safeStorageComponent(firstNonEmpty(snap.RoomID, snap.Board))
	finalPath := filepath.Join(projectDir, fmt.Sprintf("%s.json", fileKey))
	tmp, err := os.CreateTemp(projectDir, fileKey+".tmp.*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, finalPath)
}
