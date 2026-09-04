package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (t bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func TestMCPHTTPProtocolIntegration(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{"public", "private"} {
		if _, err := store.CreateRoom("default", room, room, "", "", "root"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertClientACL("agent-a", []RoomRef{{ProjectID: "default", RoomID: "public"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token: "http-token", ClientID: "agent-a", Kind: "dev-short",
		SourceIP: "127.0.0.1", IssuedAt: time.Now().Format(time.RFC3339Nano),
		ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}

	httpServer := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: store}))
	defer httpServer.Close()
	httpClient := &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: "http-token"}}
	client := mcp.NewClient(&mcp.Implementation{Name: "http-integration-test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("HTTP initialize failed: %v", err)
	}
	if session == nil {
		t.Fatal("HTTP initialize returned nil session")
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("HTTP tools/list failed: %v", err)
	}
	if len(tools.Tools) != 25 {
		t.Fatalf("tools/list returned %d tools, want 25 public tools", len(tools.Tools))
	}

	rooms, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "smalltalk_list_rooms",
		Arguments: map[string]any{"project_id": "default"},
	})
	if err != nil || rooms == nil || rooms.IsError {
		t.Fatalf("authenticated tool call failed: result=%v err=%v", rooms, err)
	}

	denied, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "smalltalk_list_messages",
		Arguments: map[string]any{"project_id": "default", "room_id": "private"},
	})
	if err != nil {
		t.Fatalf("ACL call returned protocol error: %v", err)
	}
	if denied == nil || !denied.IsError {
		t.Fatalf("ACL deny was not represented as MCP tool error: %#v", denied)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session lifecycle close failed: %v", err)
	}
}

func TestMCPHTTPUnauthenticated(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	httpServer := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: store}))
	defer httpServer.Close()
	resp, err := http.Get(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("Guest MCP connection was rejected: status=%d", resp.StatusCode)
	}
}

func TestMCPGuestReadAndWriteBoundaries(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "lobby", "Lobby", "", "", ""); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: store}))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "guest-boundary-test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, HTTPClient: http.DefaultClient, DisableStandaloneSSE: true, MaxRetries: -1}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "smalltalk_admin_") || tool.Name == "smalltalk_create_room" || tool.Name == "smalltalk_update_room" || tool.Name == "smalltalk_delete_room" {
			t.Fatalf("system tool exposed to Guest: %s", tool.Name)
		}
	}
	rooms, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_list_rooms", Arguments: map[string]any{"project_id": "default"}})
	if err != nil || rooms == nil || rooms.IsError {
		t.Fatalf("Guest read failed: result=%v err=%v", rooms, err)
	}
	write, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_create_article", Arguments: map[string]any{"project_id": "default", "room_id": "lobby", "title": "guest", "text": "must be rejected"}})
	if err != nil || write == nil || !write.IsError {
		t.Fatalf("Guest write was not rejected: result=%v err=%v", write, err)
	}
}

func TestMCPRestfulCallbackProcess(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	ensureDefaultLobby(store)
	facade := &SmallTalkFacade{Store: store}
	handler := NewMCPHTTPHandler(facade)
	callback := &mcpRestfulCallback{handler: handler}

	// Simulate MarsCloud SDK where r.Body is empty/drained and raw body string is passed in Process
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-agent","version":"1.0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()

	callback.Process(w, req, nil, nil, nil, initBody)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from mcpRestfulCallback, got %d, body: %s", resp.StatusCode, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "SmallTalk MCP Server") {
		t.Fatalf("unexpected initialize response: %s", w.Body.String())
	}
}
