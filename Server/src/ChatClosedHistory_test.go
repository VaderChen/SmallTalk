package main

import "testing"

func TestChatClosedHistoryOwnerOnly(t *testing.T) {
	h := chatHTTPFixture(t)
	a, b, guest := h.connect("local-social-alice"), h.connect("local-social-bob"), h.connect("")
	id := chatCreate(t, h.s)
	chatJoin(t, h.s, id)
	message := map[string]any{"room_id": id, "text": "成員舊訊息", "request_id": "closed-member-history"}
	ownerMessage := map[string]any{"room_id": id, "text": "發起者舊訊息", "request_id": "closed-owner-history"}
	h.call(b, "smalltalk_send_chatroom_message", message, false)
	h.call(a, "smalltalk_send_chatroom_message", ownerMessage, false)
	h.call(b, "smalltalk_read_chatroom", map[string]any{"room_id": id, "after_seq": 0}, false)
	h.call(a, "smalltalk_manage_chatroom", map[string]any{"room_id": id, "action": "close"}, false)
	h.call(a, "smalltalk_read_chatroom", map[string]any{"room_id": id, "after_seq": 0}, false)
	h.call(a, "smalltalk_chatroom_archive", map[string]any{"room_id": id, "max_bytes": 65536}, false)
	h.call(a, "smalltalk_send_chatroom_message", ownerMessage, false)
	h.call(b, "smalltalk_read_chatroom", map[string]any{"room_id": id}, true)
	h.call(b, "smalltalk_chatroom_archive", map[string]any{"room_id": id, "max_bytes": 65536}, true)
	h.call(b, "smalltalk_send_chatroom_message", message, true)
	h.call(guest, "smalltalk_read_chatroom", map[string]any{"room_id": id}, true)
	list := h.call(b, "smalltalk_list_chatrooms", nil, false)
	if len(list["chatrooms"].([]any)) != 0 {
		t.Fatal("非發起者仍列出關閉房間")
	}
	list = h.call(a, "smalltalk_list_chatrooms", nil, false)
	if len(list["chatrooms"].([]any)) != 1 {
		t.Fatal("發起者無法找到關閉房間")
	}
}
