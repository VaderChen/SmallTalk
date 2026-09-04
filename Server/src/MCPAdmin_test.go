package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPAdminAuthorizationBoundaries(t *testing.T) {
	if _, err := requireMCPRoot(context.Background()); err != ErrForbidden && err == nil {
		t.Fatalf("unauthenticated admin check error=%v", err)
	}
	ctx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{ClientID: "agent-a", PrincipalType: "agent"})
	if _, err := requireMCPRoot(ctx); err != ErrForbidden {
		t.Fatalf("non-root admin check error=%v, want %v", err, ErrForbidden)
	}
	rootCtx := context.WithValue(context.Background(), mcpPrincipalKey{}, &requestAuthContext{ClientID: "root", PrincipalType: "root"})
	if _, err := requireMCPRoot(rootCtx); err != nil {
		t.Fatalf("root admin check error=%v", err)
	}
}

func TestMCPAdminStoreOperations(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "agent-a", DisplayName: "Agent A", LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentIssuedToken("agent-a", "admin-token", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentApproval("agent-a", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.GetAgentRegistry("agent-a"); !ok {
		t.Fatal("registered agent was not retrievable")
	}
	if err := store.UpsertClientACL("agent-a", []RoomRef{{ProjectID: "default", RoomID: "public"}}, []RoomRef{{ProjectID: "default", RoomID: "private"}}); err != nil {
		t.Fatal(err)
	}
	acl, ok := store.GetClientACL("agent-a")
	if !ok || len(acl.AllowRooms) != 1 || len(acl.DenyRooms) != 1 {
		t.Fatalf("unexpected ACL: ok=%v acl=%+v", ok, acl)
	}
	if err := store.DeleteAgentRegistry("agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.GetAgentRegistry("agent-a"); ok {
		t.Fatal("deleted agent remained in registry")
	}
}

func TestAgentRoleOperations(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	clientID := "agent-hedgehog"
	displayName := "刺蝟會翻譯"
	if _, err := store.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: clientID, DisplayName: displayName, LastSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoom("default", "emei", "峨嵋派", "武俠", "峨嵋討論板", ""); err != nil {
		t.Fatal(err)
	}

	// 1. Initially not admin, not moderator
	isAdmin, modRooms, err := store.GetAgentRole(clientID)
	if err != nil || isAdmin || len(modRooms) != 0 {
		t.Fatalf("unexpected initial role: isAdmin=%v modRooms=%v err=%v", isAdmin, modRooms, err)
	}
	if store.IsBoardModerator(clientID, displayName, "default", "emei") {
		t.Fatal("should not be board moderator initially")
	}

	// 2. Set as admin and moderator of emei
	if err := store.SetAgentRole(clientID, true, []string{"default/emei"}); err != nil {
		t.Fatal(err)
	}

	isAdmin, modRooms, err = store.GetAgentRole(clientID)
	if err != nil || !isAdmin || len(modRooms) != 1 || modRooms[0] != "default/emei" {
		t.Fatalf("unexpected updated role: isAdmin=%v modRooms=%v err=%v", isAdmin, modRooms, err)
	}
	if !store.IsBoardModerator(clientID, displayName, "default", "emei") {
		t.Fatal("should be board moderator after assignment")
	}

	// 3. Remove moderator, keep admin
	if err := store.SetAgentRole(clientID, true, []string{}); err != nil {
		t.Fatal(err)
	}
	isAdmin, modRooms, err = store.GetAgentRole(clientID)
	if err != nil || !isAdmin || len(modRooms) != 0 {
		t.Fatalf("unexpected role after removing mod: isAdmin=%v modRooms=%v err=%v", isAdmin, modRooms, err)
	}
}

func TestCreateRoomFullAndAdminAPI(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	adminAPI := &PermissionsAPI{Store: store}

	// 1. Test CreateRoomFull
	room, err := store.CreateRoomFull("default", "golang", "Go語言研討", "程式語言", "討論Go語言生態", "gopher", true)
	if err != nil {
		t.Fatalf("CreateRoomFull failed: %v", err)
	}
	if room.ID != "golang" || room.Name != "Go語言研討" || !room.Pinned {
		t.Fatalf("unexpected room created: %+v", room)
	}

	// 2. Test duplicate creation returns error
	_, err = store.CreateRoomFull("default", "golang", "Go語言研討", "程式語言", "", "", false)
	if err != ErrAlreadyExists {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	// 3. Test Admin HTTP API /permissions/rooms/create
	now := time.Now()
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token:     "test-root-token",
		ClientID:  "root",
		Kind:      "session-human",
		SourceIP:  "127.0.0.1",
		IssuedAt:  now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}

	body := `{"room_id":"ai-news","name":"AI快訊","category":"人工智慧","owner":"system","pinned":true}`
	req := httptest.NewRequest(http.MethodPost, "http://example.test/permissions/rooms/create", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer test-root-token")
	w := httptest.NewRecorder()
	respBytes := adminAPI.Process(w, req, nil, nil, nil, body)
	respJSON := string(respBytes)
	if !strings.Contains(respJSON, `"ok":true`) || !strings.Contains(respJSON, `"ai-news"`) {
		t.Fatalf("unexpected API response: %s", respJSON)
	}

	// 4. Verify room exists in store
	rooms := store.ListAllRooms(time.Now())
	found := false
	for _, r := range rooms {
		if r.RoomID == "ai-news" && r.Pinned {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created room ai-news not found or not pinned in ListAllRooms")
	}
}

func TestPermissionsAPI_AdminPasswordUpdate(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	store.SetDefaultAdminPassword("root-password-123")
	adminAPI := &PermissionsAPI{Store: store}
	authAPI := &HttpAPI_auth{
		DefaultAccount:  "root",
		DefaultPassword: "root-password-123",
		Store:           store,
	}

	// Register root session token
	now := time.Now()
	rootToken := "root-session-token-999"
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token:     rootToken,
		ClientID:  "root",
		Kind:      "session-human",
		SourceIP:  "127.0.0.1",
		IssuedAt:  now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}

	callPasswordAPI := func(body string) string {
		req := httptest.NewRequest(http.MethodPost, "http://example.test/permissions/admin-password", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer "+rootToken)
		w := httptest.NewRecorder()
		return string(adminAPI.Process(w, req, nil, nil, nil, body))
	}

	// 1. Wrong old password
	respWrong := callPasswordAPI(`{"old_password":"wrong","new_password":"new-password-123","confirm_password":"new-password-123"}`)
	if !strings.Contains(respWrong, "目前密碼輸入錯誤") {
		t.Fatalf("expected wrong password error, got: %s", respWrong)
	}

	// 2. Mismatched confirm password
	respMismatch := callPasswordAPI(`{"old_password":"root-password-123","new_password":"new-password-123","confirm_password":"different-value-123"}`)
	if !strings.Contains(respMismatch, "兩次輸入的新密碼不一致") {
		t.Fatalf("expected mismatch error, got: %s", respMismatch)
	}

	// 3. Valid update
	respOK := callPasswordAPI(`{"old_password":"root-password-123","new_password":"secretAdminPassword!","confirm_password":"secretAdminPassword!"}`)
	if !strings.Contains(respOK, `"ok":true`) {
		t.Fatalf("expected success, got: %s", respOK)
	}

	// Verify store has new password
	if !store.VerifyAdminPassword("secretAdminPassword!") {
		t.Fatal("store password hash does not verify the new password")
	}

	// 4. Test /auth/login with old password -> must FAIL
	wLoginOld := httptest.NewRecorder()
	rLoginOld := httptest.NewRequest(http.MethodPost, "http://example.test/auth/login", strings.NewReader(`{"account":"root","password":"root"}`))
	rLoginOld.RemoteAddr = "127.0.0.1:1234"
	resLoginOld := string(authAPI.Process(wLoginOld, rLoginOld, nil, []string{"login"}, nil, `{"account":"root","password":"root"}`))
	if !strings.Contains(resLoginOld, "login failed") {
		t.Fatalf("expected old password login to fail, got: %s", resLoginOld)
	}

	// 5. Test /auth/login with new password -> must SUCCEED
	wLoginNew := httptest.NewRecorder()
	rLoginNew := httptest.NewRequest(http.MethodPost, "http://example.test/auth/login", strings.NewReader(`{"account":"root","password":"secretAdminPassword!"}`))
	rLoginNew.RemoteAddr = "127.0.0.1:1234"
	resLoginNew := string(authAPI.Process(wLoginNew, rLoginNew, nil, []string{"login"}, nil, `{"account":"root","password":"secretAdminPassword!"}`))
	if !strings.Contains(resLoginNew, `"ok":true`) {
		t.Fatalf("expected new password login to succeed, got: %s", resLoginNew)
	}
}
