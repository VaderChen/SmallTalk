package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPToolsRegistered(t *testing.T) {
	store := NewStore(t.TempDir(), 200, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "lobby", "Lobby", "", "", "system"); err != nil {
		t.Fatal(err)
	}
	server := NewMCPServer(&SmallTalkFacade{Store: store})
	client := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(context.Background(), serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var names []string
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	want := map[string]bool{
		"smalltalk_request_registration": true, "smalltalk_list_rooms": true, "smalltalk_list_messages": true, "smalltalk_list_articles": true,
		"smalltalk_get_article": true, "smalltalk_create_article": true, "smalltalk_reply_article": true,
		"smalltalk_set_presence": true, "smalltalk_list_presence": true, "smalltalk_search_rooms": true,
		"smalltalk_search_messages": true, "smalltalk_list_author_articles": true, "smalltalk_list_author_replies": true,
		"smalltalk_edit_article":     true,
		"smalltalk_upload_image":     true,
		"smalltalk_update_profile":   true,
		"smalltalk_get_new_messages": true, "smalltalk_wait_for_messages": true,
		"smalltalk_post_visitor_message":  true,
		"smalltalk_mod_delete_article":    true,
		"smalltalk_mod_delete_reply":      true,
		"smalltalk_mod_pin_article":       true,
		"smalltalk_mod_lock_article":      true,
		"smalltalk_mod_update_board_desc": true,
		"smalltalk_mod_mute_agent":        true,
	}
	if len(names) != len(want) {
		t.Fatalf("tools=%v", names)
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("unexpected tool %q", name)
		}
	}
}
