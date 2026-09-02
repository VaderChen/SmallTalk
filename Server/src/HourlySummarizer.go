package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

type mhSourceRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}

type mhUpsertReq struct {
	UpsertKey  string        `json:"upsert_key"`
	ProjectID  *string       `json:"project_id"`
	Type       string        `json:"type"`
	Title      string        `json:"title"`
	ContentMD  string        `json:"content_md"`
	Tags       []string      `json:"tags"`
	SourceRefs []mhSourceRef `json:"source_refs"`
	Importance int           `json:"importance"`
	Tier       string        `json:"tier"`
	ExpiresAt  *string       `json:"expires_at,omitempty"`
	Pin        bool          `json:"pin,omitempty"`
	Actor      string        `json:"actor"`
}

func StartHourlySummarizer(store *Store, memoryHubURL string, interval time.Duration) func() {
	memoryHubURL = strings.TrimSpace(memoryHubURL)
	if memoryHubURL == "" {
		Tools.Log.Print(Tools.LL_Info, "Hourly summarizer disabled: memoryhub_url is empty")
		return nil
	}
	if interval <= 0 {
		interval = time.Hour
	}

	stop := make(chan struct{})
	go func() {
		// Align to the next interval boundary (hourly by default)
		for {
			now := time.Now()
			next := now.Truncate(interval).Add(interval)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}

			end := time.Now().Truncate(interval)
			start := end.Add(-interval)
			summarizeWindow(store, memoryHubURL, start, end)
		}
	}()

	Tools.Log.Print(Tools.LL_Info, "Hourly summarizer enabled: interval=%s -> MemoryHub=%s", interval.String(), memoryHubURL)
	return func() { close(stop) }
}

func summarizeWindow(store *Store, memoryHubURL string, start, end time.Time) {
	snaps := store.SnapshotTaskRooms()
	for _, rr := range snaps {
		msgs := filterMsgs(rr.Messages, start, end)
		if len(msgs) == 0 {
			continue
		}

		taskID := rr.TaskID
		proj := rr.ProjectID
		room := rr.RoomID

		upsertKey := fmt.Sprintf("smalltalk:%s:%s:%s", proj, room, end.Format("2006010215"))
		title := fmt.Sprintf("SmallTalk Hourly Summary: %s/%s @ %s", proj, room, end.Format("2006-01-02 15:00"))

		content := buildDigestMarkdown(start, end, proj, room, taskID, msgs)
		tags := []string{"smalltalk", "hourly", "taskhub", "task:" + taskID}

		pid := proj
		exp := end.Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
		req := mhUpsertReq{
			UpsertKey:  upsertKey,
			ProjectID:  &pid,
			Type:       "summary",
			Title:      title,
			ContentMD:  content,
			Tags:       tags,
			SourceRefs: []mhSourceRef{{Kind: "smalltalk", Ref: fmt.Sprintf("project:%s/room:%s/window:%s~%s", proj, room, start.Format(time.RFC3339), end.Format(time.RFC3339))}, {Kind: "taskhub", Ref: "task_id:" + taskID}},
			Importance: 30,
			Tier:       "stm",
			ExpiresAt:  &exp,
			Actor:      "system:smalltalk",
		}
		postToMemoryHub(memoryHubURL, req)

		// Also write a rolling LTM condensed summary for the task-room.
		writeRollingLTM(memoryHubURL, proj, room, taskID, rr.Messages)
	}
}

type TaskRoomSnapshot struct {
	ProjectID string
	RoomID    string
	TaskID    string
	Messages  []Message
}

func (s *Store) SnapshotTaskRooms() []TaskRoomSnapshot {
	s.mu.RLock()
	type taskTarget struct {
		pid    string
		rid    string
		taskID string
		r      *Room
	}
	var targets []taskTarget
	for pid, p := range s.projects {
		for rid, r := range p.Rooms {
			taskID := extractTaskID(rid)
			if taskID == "" {
				continue
			}
			targets = append(targets, taskTarget{pid: pid, rid: rid, taskID: taskID, r: r})
		}
	}
	s.mu.RUnlock()

	out := make([]TaskRoomSnapshot, 0, len(targets))
	for _, t := range targets {
		t.r.mu.RLock()
		msgs := make([]Message, len(t.r.Messages))
		copy(msgs, t.r.Messages)
		t.r.mu.RUnlock()
		out = append(out, TaskRoomSnapshot{ProjectID: t.pid, RoomID: t.rid, TaskID: t.taskID, Messages: msgs})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].RoomID < out[j].RoomID
	})
	return out
}

func extractTaskID(roomID string) string {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ""
	}
	// recommended pattern: task-<task_id>
	if strings.HasPrefix(roomID, "task-") {
		return strings.TrimPrefix(roomID, "task-")
	}
	// fallback: contains task-
	idx := strings.Index(roomID, "task-")
	if idx >= 0 {
		return roomID[idx+5:]
	}
	return ""
}

func filterMsgs(in []Message, start, end time.Time) []Message {
	out := make([]Message, 0)
	for _, m := range in {
		if m.TS.IsZero() {
			continue
		}
		if !m.TS.Before(start) && m.TS.Before(end) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out
}

func buildDigestMarkdown(start, end time.Time, projectID, roomID, taskID string, msgs []Message) string {
	agents := map[string]int{}
	for _, m := range msgs {
		agents[m.AgentID]++
	}
	agentsList := make([]string, 0, len(agents))
	for a := range agents {
		agentsList = append(agentsList, a)
	}
	sort.Strings(agentsList)

	// Include last N messages as evidence
	max := 30
	if len(msgs) < max {
		max = len(msgs)
	}
	last := msgs[len(msgs)-max:]

	b := &strings.Builder{}
	fmt.Fprintf(b, "## Time Window\n- from: %s\n- to: %s\n\n", start.Format(time.RFC3339), end.Format(time.RFC3339))
	fmt.Fprintf(b, "## Context\n- project: %s\n- room: %s\n- task_id: %s\n\n", projectID, roomID, taskID)
	fmt.Fprintf(b, "## Activity\n- messages: %d\n- units: %d\n\n", len(msgs), len(agents))
	fmt.Fprintf(b, "## Units\n")
	for _, a := range agentsList {
		fmt.Fprintf(b, "- %s (%d)\n", a, agents[a])
	}
	// Heuristic summary extraction (no LLM): lines prefixed with keywords.
	changed, blockers, decisions, next := extractProgressItems(msgs)

	fmt.Fprintf(b, "\n## Progress Summary\n")
	fmt.Fprintf(b, "- what changed:\n")
	writeList(b, changed)
	fmt.Fprintf(b, "- current blockers:\n")
	writeList(b, blockers)
	fmt.Fprintf(b, "- decisions made:\n")
	writeList(b, decisions)
	fmt.Fprintf(b, "- next actions:\n")
	writeList(b, next)

	fmt.Fprintf(b, "\n## Evidence (last %d messages)\n", max)
	for _, m := range last {
		fmt.Fprintf(b, "- [%s] **%s**: %s\n", m.TS.Format("15:04"), safeUnit(m.AgentID), oneLine(m.Text, 240))
	}

	return b.String()
}

func extractProgressItems(msgs []Message) (changed []string, blockers []string, decisions []string, next []string) {
	add := func(dst *[]string, s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		*dst = append(*dst, s)
	}

	for _, m := range msgs {
		lines := strings.Split(m.Text, "\n")
		for _, ln := range lines {
			l := strings.TrimSpace(ln)
			if l == "" {
				continue
			}
			u := strings.ToUpper(l)
			switch {
			case strings.HasPrefix(u, "CHANGED:") || strings.HasPrefix(u, "CHANGE:") || strings.HasPrefix(u, "UPDATE:"):
				add(&changed, strings.TrimSpace(l[strings.Index(l, ":")+1:]))
			case strings.HasPrefix(u, "BLOCKER:") || strings.HasPrefix(u, "BLOCK:"):
				add(&blockers, strings.TrimSpace(l[strings.Index(l, ":")+1:]))
			case strings.HasPrefix(u, "DECISION:") || strings.HasPrefix(u, "DECIDE:"):
				add(&decisions, strings.TrimSpace(l[strings.Index(l, ":")+1:]))
			case strings.HasPrefix(u, "NEXT:") || strings.HasPrefix(u, "TODO:") || strings.HasPrefix(u, "ACTION:"):
				add(&next, strings.TrimSpace(l[strings.Index(l, ":")+1:]))
			}
		}
	}

	// Dedup preserving order
	dedup := func(in []string) []string {
		seen := map[string]bool{}
		out := make([]string, 0, len(in))
		for _, s := range in {
			k := strings.ToLower(strings.TrimSpace(s))
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, s)
		}
		return out
	}
	return dedup(changed), dedup(blockers), dedup(decisions), dedup(next)
}

func writeList(b *strings.Builder, items []string) {
	if len(items) == 0 {
		fmt.Fprintf(b, "  - -\n")
		return
	}
	for _, it := range items {
		fmt.Fprintf(b, "  - %s\n", oneLine(it, 240))
	}
}

func safeUnit(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return s
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if max > 0 && len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func writeRollingLTM(memoryHubURL, projectID, roomID, taskID string, msgs []Message) {
	if projectID == "" || roomID == "" || taskID == "" {
		return
	}

	changed, blockers, decisions, next := extractProgressItems(msgs)
	if len(changed) == 0 && len(blockers) == 0 && len(decisions) == 0 && len(next) == 0 {
		// no structured progress; keep it quiet
		return
	}

	upsertKey := fmt.Sprintf("smalltalk:%s:%s:ltm", projectID, roomID)
	title := fmt.Sprintf("SmallTalk LTM: %s/%s (task:%s)", projectID, roomID, taskID)

	content := buildLTMCondensedMarkdown(projectID, roomID, taskID, changed, blockers, decisions, next)

	pid := projectID
	req := mhUpsertReq{
		UpsertKey:  upsertKey,
		ProjectID:  &pid,
		Type:       "summary",
		Title:      title,
		ContentMD:  content,
		Tags:       []string{"smalltalk", "ltm", "taskhub", "task:" + taskID},
		SourceRefs: []mhSourceRef{{Kind: "smalltalk", Ref: fmt.Sprintf("project:%s/room:%s", projectID, roomID)}, {Kind: "taskhub", Ref: "task_id:" + taskID}},
		Importance: 60,
		Tier:       "ltm",
		Actor:      "system:smalltalk",
	}

	postToMemoryHub(memoryHubURL, req)
}

func buildLTMCondensedMarkdown(projectID, roomID, taskID string, changed, blockers, decisions, next []string) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "## Context\n- project: %s\n- room: %s\n- task_id: %s\n- updated_at: %s\n\n", projectID, roomID, taskID, time.Now().Format(time.RFC3339Nano))

	fmt.Fprintf(b, "## What changed\n")
	writeList(b, changed)
	fmt.Fprintf(b, "\n## Blockers\n")
	writeList(b, blockers)
	fmt.Fprintf(b, "\n## Decisions\n")
	writeList(b, decisions)
	fmt.Fprintf(b, "\n## Next actions\n")
	writeList(b, next)
	return b.String()
}

func postToMemoryHub(memoryHubURL string, req mhUpsertReq) {
	b, _ := json.Marshal(req)
	client := &http.Client{Timeout: 10 * time.Second}
	url := strings.TrimRight(memoryHubURL, "/") + "/api/memories"
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		Tools.Log.Print(Tools.LL_Error, "MemoryHub write failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		Tools.Log.Print(Tools.LL_Error, "MemoryHub write failed: status=%d", resp.StatusCode)
		return
	}
}
