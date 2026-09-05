package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

const minimumSelfRestartUptime = time.Minute

func normalizeSelfRestartTimes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		parts := strings.Split(strings.TrimSpace(value), ":")
		if len(parts) < 2 {
			continue
		}
		hour, hourErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		minute, minuteErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			continue
		}
		normalized := fmt.Sprintf("%02d:%02d", hour, minute)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func resolveSelfRestartLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Local
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		Tools.Log.Print(Tools.LL_Warning, "Invalid restart_timezone %q for SmallTalk self-restart; using local time", name)
		return time.Local
	}
	return location
}

func startSelfRestartScheduler(values []string, timezone string) (<-chan string, func()) {
	times := normalizeSelfRestartTimes(values)
	requests := make(chan string, 1)
	if len(times) == 0 {
		return requests, nil
	}

	location := resolveSelfRestartLocation(timezone)
	stop := make(chan struct{})
	var stopOnce sync.Once
	startedAt := time.Now()
	Tools.Log.Print(Tools.LL_Info, "SmallTalk self-restart at: %v", times)

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastMinute := ""
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				if now.Sub(startedAt) < minimumSelfRestartUptime {
					continue
				}
				minute := now.In(location).Format("15:04")
				if minute == lastMinute {
					continue
				}
				for _, target := range times {
					if minute != target {
						continue
					}
					lastMinute = minute
					select {
					case requests <- target:
					case <-stop:
					}
					return
				}
			}
		}
	}()

	return requests, func() {
		stopOnce.Do(func() { close(stop) })
	}
}

func validateSelfRestart() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("stat executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path is a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("executable path is not executable")
	}
	return nil
}
