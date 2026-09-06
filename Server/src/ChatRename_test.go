package main

import (
	"strings"
	"testing"
)

func TestChatRenameMCP(t *testing.T) {
	h := chatHTTPFixture(t)
	a, b := h.connect("local-social-alice"), h.connect("local-social-bob")
	create := map[string]any{"name": "原房名", "request_id": "rename-create", "join_mode": "public"}
	room := h.call(a, "smalltalk_create_chatroom", create, false)
	id := room["room_id"].(string)
	h.call(b, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "join"}, false)
	h.call(b, "smalltalk_send_chatroom_message", map[string]any{"room_id": id, "text": "既有訊息", "request_id": "before-rename"}, false)
	rename := func(name string) map[string]any {
		return map[string]any{"room_id": id, "action": "rename", "name": name}
	}
	h.call(b, "smalltalk_manage_chatroom", rename("越權"), true)
	for _, name := range []string{" ", "名稱\n換行", strings.Repeat("字", 81)} {
		h.call(a, "smalltalk_manage_chatroom", rename(name), true)
	}
	changed := h.call(a, "smalltalk_manage_chatroom", rename(" 新房名 "), false)
	if changed["room_id"] != id || changed["name"] != "新房名" || changed["join_mode"] != "public" {
		t.Fatal("改名變更其他欄位")
	}
	again := h.call(a, "smalltalk_manage_chatroom", rename("新房名"), false)
	if again["unchanged"] != true || again["last_seq"] != changed["last_seq"] {
		t.Fatal("同名重試新增事件")
	}
	duplicate := h.call(a, "smalltalk_create_chatroom", create, false)
	if duplicate["room_id"] != id || duplicate["name"] != "新房名" || duplicate["duplicate"] != true {
		t.Fatal("原建立去重失效")
	}
	create["name"] = "新房名"
	h.call(a, "smalltalk_create_chatroom", create, true)
	page := h.call(b, "smalltalk_read_chatroom", map[string]any{"room_id": id}, false)
	records := page["records"].([]any)
	last := records[len(records)-1].(map[string]any)
	if last["kind"] != "rename" || last["old_name"] != "原房名" || last["new_name"] != "新房名" {
		t.Fatal("缺改名稽核事件")
	}
	if records[len(records)-2].(map[string]any)["text"] != "既有訊息" {
		t.Fatal("既有訊息改變")
	}
	h.call(a, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "close"}, false)
	h.call(a, "smalltalk_manage_chatroom", rename("已關閉改名"), true)
}
