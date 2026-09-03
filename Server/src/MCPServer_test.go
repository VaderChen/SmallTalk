package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPToolErrorsAndACL(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "public", "Public", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "private", "Private", "", "", "root"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClientACL("agent-a", []RoomRef{{ProjectID: "default", RoomID: "public"}}, nil); err != nil {
		t.Fatal(err)
	}

	server := NewMCPServer(&SmallTalkFacade{Store: store})
	client := mcp.NewClient(&mcp.Implementation{Name: "tool-test", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go server.Connect(context.Background(), serverTransport, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// A handler context is not available through the in-memory protocol, so test the
	// protocol input validation and facade ACL boundary directly as well.
	if _, err := (&SmallTalkFacade{Store: store}).ListMessages("agent-a", "default", "private", MessagePageOptions{}); err != ErrForbidden {
		t.Fatalf("private room error=%v, want forbidden", err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "smalltalk_list_rooms", Arguments: map[string]any{}})
	if err == nil && result != nil && !result.IsError {
		t.Fatal("missing project_id should be rejected")
	}
	if result != nil {
		_, _ = json.Marshal(result)
	}
}

func TestMCPListenerLifecycle(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.CreateProject("default", "Default"); err != nil {
		t.Fatal(err)
	}
	port := freeTCPPort(t)
	listeners := startMCPListeners(store, port, 0, "", "")
	defer listeners.Shutdown(time.Second)
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/mcp", port))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("MCP listener did not start: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("Guest MCP request was rejected by auth middleware: status=%d", resp.StatusCode)
	}
	if err := listeners.Shutdown(time.Second); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("MCP port was not released: %v", err)
	}
	probe.Close()
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func TestMCPRegistrationInputValidation(t *testing.T) {
	valid := &mcpRegistrationInput{DisplayName: "Demo Agent", MACAddress: "aa:bb:cc:dd:ee:ff"}
	if err := validateMCPRegistrationInput(valid); err != nil {
		t.Fatalf("valid registration rejected: %v", err)
	}
	if valid.MACAddress != "AABBCCDDEEFF" {
		t.Fatalf("MAC normalization failed: %q", valid.MACAddress)
	}
	for _, in := range []*mcpRegistrationInput{
		{DisplayName: "", MACAddress: "AABBCCDDEEFF"},
		{DisplayName: "Demo", MACAddress: "not-a-mac"},
	} {
		if err := validateMCPRegistrationInput(in); err == nil {
			t.Fatalf("invalid registration accepted: %#v", in)
		}
	}
}

func TestMCPGuestRegistrationRequest(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	httpServer := httptest.NewServer(NewMCPHTTPHandler(&SmallTalkFacade{Store: store}))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "registration-test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, HTTPClient: http.DefaultClient, DisableStandaloneSSE: true, MaxRetries: -1}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"display_name": "Guest Agent", "mac_address": "AA:BB:CC:DD:EE:FF",
	}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("guest registration failed: result=%v err=%v", result, err)
	}
	var response struct {
		ClientID string `json:"client_id"`
	}
	if len(result.Content) != 1 {
		t.Fatalf("unexpected registration response: %#v", result)
	}
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &response); err != nil {
		t.Fatal(err)
	}
	if response.ClientID == "" || !strings.HasPrefix(response.ClientID, "agent-ddeeff-") {
		t.Fatalf("system did not assign client_id with MAC suffix: %s, %#v", response.ClientID, result)
	}
	entry, ok := store.GetAgentRegistry(response.ClientID)
	if !ok || entry.Approved || entry.TokenIssued || entry.Token != "" || entry.MACAddress != "AABBCCDDEEFF" || entry.DisplayName != "Guest Agent" {
		t.Fatalf("unsafe registration record: %#v found=%v", entry, ok)
	}

	conflict, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"display_name": "Changed", "mac_address": "11:22:33:44:55:66",
	}})
	if err != nil || conflict == nil || conflict.IsError {
		t.Fatalf("second registration failed: result=%v err=%v", conflict, err)
	}
	var second struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(conflict.Content[0].(*mcp.TextContent).Text), &second); err != nil {
		t.Fatal(err)
	}
	if second.ClientID == "" || !strings.HasPrefix(second.ClientID, "agent-445566-") || second.ClientID == response.ClientID {
		t.Fatalf("system-assigned IDs are not unique or properly formatted: first=%q second=%q", response.ClientID, second.ClientID)
	}

	// 3. Test: Agent forgot ID, didn't provide MAC, but uses same display_name "Guest Agent"
	reconnect, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"display_name": "Guest Agent",
	}})
	if err != nil || reconnect == nil || reconnect.IsError {
		t.Fatalf("reconnect registration failed: result=%v err=%v", reconnect, err)
	}
	var third struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(reconnect.Content[0].(*mcp.TextContent).Text), &third); err != nil {
		t.Fatal(err)
	}
	if third.ClientID != response.ClientID {
		t.Fatalf("expected existing client_id %q to be reused, got new ID %q", response.ClientID, third.ClientID)
	}

	// 4. Test: Try to rename agent to an already-taken display_name ("Changed") -> MUST BE BLOCKED!
	renameConflict, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"client_id":    response.ClientID,
		"display_name": "Changed", // already taken by second agent!
	}})
	if err == nil && (renameConflict != nil && !renameConflict.IsError) {
		t.Fatalf("expected rename to conflicting display_name to be blocked, but succeeded: %#v", renameConflict)
	}

	// 5. Test: Try to rename agent to a new, unused display_name -> MUST SUCCEED!
	renameSuccess, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "smalltalk_request_registration", Arguments: map[string]any{
		"client_id":    response.ClientID,
		"display_name": "Brand New Unique Name",
	}})
	if err != nil || renameSuccess == nil || renameSuccess.IsError {
		t.Fatalf("expected valid rename to succeed: err=%v result=%#v", err, renameSuccess)
	}
}
