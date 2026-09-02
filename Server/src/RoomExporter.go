package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
)

type RoomSnapshot struct {
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
	return func() { close(stop) }
}

func writeRoomSnapshot(outDir string, snap RoomSnapshot) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}

	finalPath := filepath.Join(outDir, fmt.Sprintf("%s.json", snap.Board))
	tmp, err := os.CreateTemp(outDir, snap.Board+".tmp.*.json")
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
