package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicChatLiveReadOnly(t *testing.T) {
	s := chatFixture(t, nil)
	id := chatCreate(t, s)
	chatJoin(t, s, id)
	text := "第一行\n<script>alert('x')</script>\n第三行"
	if _, err := s.SendChatMessage("alice", id, text, "private-request-key"); err != nil {
		t.Fatal(err)
	}
	api := &BBSAPI{Store: s}
	call := func(method, path string) (int, map[string]any, string) {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/chatrooms"+path, nil)
		w := httptest.NewRecorder()
		body := api.Process(w, r, nil, nil, nil, "")
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatal(err)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("public response must not be cached")
		}
		return w.Code, data, string(body)
	}
	code, list, raw := call("GET", "")
	if code != 200 || len(list["chatrooms"].([]any)) != 1 {
		t.Fatalf("list %d %s", code, raw)
	}
	code, data, raw := call("GET", "/"+id)
	if code != 200 {
		t.Fatalf("read %d %s", code, raw)
	}
	messages := data["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["text"] != text {
		t.Fatalf("messages %s", raw)
	}
	for _, field := range []string{"request_id", "private-request-key", "directory", "filename", "members", "target_id", "retain_until"} {
		if strings.Contains(raw, field) {
			t.Fatalf("公開回應洩漏 %s", field)
		}
	}
	_, page, _ := call("GET", "/"+id+"?limit=1")
	if len(page["messages"].([]any)) != 0 || page["next_after_seq"].(float64) != 1 || page["has_more"] != true {
		t.Fatal("事件分頁游標錯誤")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD"} {
		code, _, _ := call(method, "/"+id)
		if code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %d", method, code)
		}
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=x", "/" + id + "?after_seq=-1", "/" + id + "?after_seq=x"} {
		code, _, _ := call("GET", query)
		if code != 400 {
			t.Fatalf("query %s status %d", query, code)
		}
	}
	if _, e := s.ManageChatroom("alice", id, "close", ""); e != nil {
		t.Fatal(e)
	}
	code, list, _ = call("GET", "")
	if code != 200 || len(list["chatrooms"].([]any)) != 0 {
		t.Fatal("關閉聊天室仍列出")
	}
	code, _, _ = call("GET", "/"+id)
	if code != 404 {
		t.Fatal("關閉後仍可公开讀取")
	}
	if _, e := s.ReadChatroom("alice", id, 0, 50); e != nil {
		t.Fatal("原成員MCP歷史權限受損", e)
	}
}
