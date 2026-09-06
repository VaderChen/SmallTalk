package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatPublicJoinAndPresence(t *testing.T) {
	s := chatFixture(t, nil)
	room, err := s.CreateChatroomWithMode("alice", "公開聊天", "public-room", "public")
	if err != nil {
		t.Fatal(err)
	}
	id := room["room_id"].(string)
	if _, err = s.CreateChatroomWithMode("alice", "公開聊天", "public-room", "invite"); err == nil {
		t.Fatal("去重不得改模式")
	}
	if _, err = s.SetChatPresence("bob", id, "online"); err == nil {
		t.Fatal("未加入不能心跳")
	}
	if _, err = s.ManageChatroom("bob", id, "join", ""); err != nil {
		t.Fatal("無好友也可公開加入", err)
	}
	if _, err = s.SendChatMessage("bob", id, "公開聊天室訊息", "public-message"); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/chatrooms/"+id, nil)
		raw := (&BBSAPI{Store: s}).Process(w, req, nil, nil, nil, "")
		var out map[string]any
		if e := json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		if w.Code != 200 {
			t.Fatalf("read %d %s", w.Code, raw)
		}
		return int(out["active_participant_count"].(float64))
	}
	if count() != 0 {
		t.Fatal("歷史發言、名冊與網頁讀取不應增加人數")
	}
	for _, agent := range []string{"alice", "bob", "bob"} {
		if _, err = s.SetChatPresence(agent, id, "online"); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 2 {
		t.Fatal("心跳按帳號去重，發起者不例外")
	}
	s.mu.Lock()
	s.chatPresence[id]["bob"] = time.Now().Add(-chatPresenceTTL)
	s.mu.Unlock()
	if count() != 1 {
		t.Fatal("TTL應失效")
	}
	if _, err = s.SetChatPresence("bob", id, "online"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageChatroom("bob", id, "leave", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageChatroom("bob", id, "join", ""); err != nil {
		t.Fatal("公開可自由離開再加入", err)
	}
	if count() != 1 {
		t.Fatal("重新加入不得復活舊心跳")
	}
	if _, err = s.SetChatPresence("bob", id, "online"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageChatroom("alice", id, "remove", "bob"); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("被移除立即不計入")
	}
	if _, err = s.ManageChatroom("bob", id, "join", ""); err == nil {
		t.Fatal("不可繞過發起者移除")
	}
	if _, err = s.SetChatPresence("alice", id, "offline"); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("offline未清除")
	}
	list, err := s.ListChatroomsScope("bob", "", 50, "public")
	if err != nil || len(list["chatrooms"].([]map[string]any)) != 1 {
		t.Fatal("公開房發現", err)
	}
	if _, err = s.ManageChatroom("alice", id, "close", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetChatPresence("alice", id, "online"); err == nil {
		t.Fatal("關閉不能心跳")
	}
	list, err = s.ListChatroomsScope("bob", "", 50, "public")
	if err != nil || len(list["chatrooms"].([]map[string]any)) != 0 {
		t.Fatal("closed不得列出", err)
	}
}
func TestChatInviteDefaultUnchanged(t *testing.T) {
	s := chatFixture(t, nil)
	id := chatCreate(t, s)
	if _, err := s.ManageChatroom("bob", id, "join", ""); err == nil {
		t.Fatal("預設邀請房不能自行加入")
	}
	if chatJoinMode(AgentChatroom{}) != "invite" {
		t.Fatal("舊資料預設邀請")
	}
}

func TestChatPublicJoinMCP(t *testing.T) {
	h := chatHTTPFixture(t)
	a, b, guest := h.connect("local-social-alice"), h.connect("local-social-bob"), h.connect("")
	room := h.call(a, "smalltalk_create_chatroom", map[string]any{"name": "自由加入", "request_id": "public-http", "join_mode": "public"}, false)
	id := room["room_id"].(string)
	if room["join_mode"] != "public" {
		t.Fatal("MCP未帶入模式")
	}
	h.call(guest, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "join"}, true)
	h.call(guest, "smalltalk_chatroom_presence", map[string]any{"room_id": id, "status": "online"}, true)
	h.call(b, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "join"}, false)
	presence := h.call(b, "smalltalk_chatroom_presence", map[string]any{"room_id": id, "status": "online"}, false)
	if presence["active_participant_count"].(float64) != 1 {
		t.Fatal("MCP心跳未生效")
	}
	h.call(b, "smalltalk_send_chatroom_message", map[string]any{"room_id": id, "text": "無需好友即可加入發言", "request_id": "public-send"}, false)
	h.call(b, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "leave"}, false)
	h.call(b, "smalltalk_send_chatroom_message", map[string]any{"room_id": id, "text": "離開後不能發言", "request_id": "public-left"}, true)
	h.s.mu.Lock()
	h.s.agentRegistry["bob"].ReadOnly = true
	h.s.mu.Unlock()
	h.call(b, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "join"}, true)
	h.call(b, "smalltalk_chatroom_presence", map[string]any{"room_id": id, "status": "online"}, true)
}
