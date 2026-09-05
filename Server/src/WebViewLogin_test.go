package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func seedViewAgent(t *testing.T, s *Store) string {
	t.Helper()
	token := "isolated-view-agent-token"
	if _, err := s.UpsertAgentRegistry(AgentRegistryUpsert{ClientID: "view-agent", DisplayName: "唯讀授權測試"}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	entry := s.agentRegistry["view-agent"]
	entry.Approved = true
	entry.IsAdmin = true
	entry.Token = token
	s.mu.Unlock()
	if err := s.SaveRegistry(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAuthToken(AuthTokenRecord{Token: token, ClientID: "view-agent", Kind: "agent", IssuedAt: time.Now().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(48 * time.Hour).Format(time.RFC3339Nano)}, false); err != nil {
		t.Fatal(err)
	}
	return token
}
func TestWebViewLocalHTTPSmoke(t *testing.T) {
	s := NewStore(t.TempDir(), 100, false)
	token := seedViewAgent(t, s)
	facade := &SmallTalkFacade{Store: s}
	auth := &HttpAPI_auth{Store: s}
	mux := http.NewServeMux()
	mux.Handle("/mcp/", NewMCPHTTPHandler(facade))
	mux.HandleFunc("/auth/", func(w http.ResponseWriter, r *http.Request) { w.Write(auth.Process(w, r, nil, nil, nil, "")) })
	bbs := &BBSAPI{Store: s}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { w.Write(bbs.Process(w, r, nil, nil, nil, "{}")) })
	server := httptest.NewServer(mux)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	post := func(path, origin string) (map[string]any, *http.Response) {
		t.Helper()
		r, _ := http.NewRequest("POST", server.URL+path, strings.NewReader("{}"))
		r.Header.Set("Origin", origin)
		resp, err := browser.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out, resp
	}
	if _, r := post("/auth/view/request", "https://other.invalid"); r.StatusCode != 403 {
		t.Fatal("跨站建立未拒絕")
	}
	if data, _ := post("/auth/view/resume", server.URL); data["status"] != "idle" {
		t.Fatal("未建立請求的恢復狀態不符", data)
	}
	data, _ := post("/auth/view/request", server.URL)
	id, ok := data["request_id"].(string)
	if !ok {
		t.Fatal(data)
	}
	if data, _ := post("/auth/view/poll", server.URL); data["status"] != "pending" {
		t.Fatal(data)
	}
	// 模擬重新整理後只保留 HttpOnly cookie，仍找回同一筆待核准請求。
	resumed, _ := post("/auth/view/resume", server.URL)
	if resumed["status"] != "pending" || resumed["request_id"] != id || resumed["expires_at"] != data["expires_at"] {
		t.Fatal("恢復請求更換ID或延長期限", resumed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func(tok string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "view-local", Version: "1"}, nil)
		sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp/", HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport, token: tok}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { sess.Close() })
		return sess
	}
	agent := connect(token)
	call := func(session *mcp.ClientSession, name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result == nil || result.IsError != wantError {
			t.Fatalf("%s: error=%v result=%+v", name, err, result)
		}
		if wantError {
			return nil
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	link := server.URL + "/agent-view.html#request=" + id
	call(agent, "smalltalk_approve_browser_view", map[string]any{"approval_url": link}, false)
	call(agent, "smalltalk_approve_browser_view", map[string]any{"approval_url": link}, false)
	// 只有連結而沒有原瀏覽器 cookie，不能完成登入。
	outsider, _ := http.NewRequest("POST", server.URL+"/auth/view/poll", strings.NewReader("{}"))
	outsider.Header.Set("Origin", server.URL)
	outResp, err := http.DefaultClient.Do(outsider)
	if err != nil {
		t.Fatal(err)
	}
	if len(outResp.Cookies()) != 0 {
		t.Fatal("其他瀏覽器取得憑證")
	}
	outResp.Body.Close()
	data, resp := post("/auth/view/poll", server.URL)
	if data["status"] != "approved" || data["read_only"] != true {
		t.Fatal(data)
	}
	var viewToken string
	for _, c := range resp.Cookies() {
		if c.Name == "smalltalk_auth_token" {
			if !c.HttpOnly || c.MaxAge > 86400 {
				t.Fatal("不安全 cookie")
			}
			viewToken = c.Value
		}
	}
	if viewToken == "" {
		t.Fatal("未核發唯讀 session")
	}
	// 借用完整 Agent 的 MCP transport session，仍只能依當次憑證唯讀。
	for _, authToken := range []string{viewToken, ""} {
		raw := `{"jsonrpc":"2.0","id":9999,"method":"tools/call","params":{"name":"smalltalk_admin_list_agents","arguments":{}}}`
		request, _ := http.NewRequest("POST", server.URL+"/mcp/", strings.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Mcp-Session-Id", agent.ID())
		if authToken != "" {
			request.Header.Set("Authorization", "Bearer "+authToken)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var rpc struct {
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&rpc); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if rpc.Error == nil && !rpc.Result.IsError {
			t.Fatal("舊 MCP session 洩漏管理權限")
		}
	}
	view := connect(viewToken)
	status := call(view, "smalltalk_auth_status", map[string]any{}, false)
	if status["write_access"] != false || status["session_read_only"] != true || status["principal_type"] == "root" {
		t.Fatal(status)
	}
	profile := call(view, "smalltalk_account_profile", map[string]any{}, false)
	if profile["can_rename"] != false || profile["read_only"] != true {
		t.Fatal(profile)
	}
	// 包含原本允許 Guest 的寫入與憑證操作，必須一律拒絕。
	for _, name := range []string{"smalltalk_update_profile", "smalltalk_create_article", "smalltalk_reply_article", "smalltalk_post_visitor_message", "smalltalk_set_presence", "smalltalk_request_registration", "smalltalk_request_email_binding", "smalltalk_request_token_recovery", "smalltalk_complete_email_verification", "smalltalk_upload_image", "smalltalk_approve_browser_view"} {
		call(view, name, map[string]any{}, true)
	}
	if _, r := post("/api/boards/visitors/messages", server.URL); r.StatusCode != 403 {
		t.Fatal("HTTP 寫入未拒絕")
	}
	if _, r := post("/auth/devRegister", server.URL); r.StatusCode != 403 {
		t.Fatal("HTTP 帳號寫入未拒絕")
	}
	req := httptest.NewRequest("GET", "http://example.test/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+viewToken)
	if requireHTTPRoot(req, nil, s) == nil {
		t.Fatal("臨時登入繼承管理員權限")
	}
	again, _ := post("/auth/view/poll", server.URL)
	if again["expires_at"] != data["expires_at"] {
		t.Fatal("輪詢延長登入期限")
	}
	restarted := NewStore(s.dataDir, 100, false)
	if _, ok := restarted.authorizeViewToken(viewToken); !ok {
		t.Fatal("重啟遺失 session")
	}
	if _, r := post("/auth/logout", server.URL); r.StatusCode != 200 {
		t.Fatal("登出失敗")
	}
	if _, ok := s.authorizeViewToken(viewToken); ok {
		t.Fatal("登出未撤銷 session")
	}
	// 撤銷後的 cookie 不能退回 Guest 發文。
	u, _ := url.Parse(server.URL)
	browser.Jar.SetCookies(u, []*http.Cookie{{Name: "smalltalk_auth_token", Value: viewToken, Path: "/"}})
	if _, r := post("/api/boards/visitors/messages", server.URL); r.StatusCode != 403 {
		t.Fatal("失效 session 退回訪客寫入")
	}
	for _, path := range []string{"/auth/login", "/auth/devRegister", "/auth/devLogin", "/auth/email/bind", "/auth/email/complete", "/auth/email/recovery"} {
		if _, response := post(path, server.URL); response.StatusCode != http.StatusForbidden {
			t.Fatalf("失效唯讀憑證進入 %s", path)
		}
	}
	// 完整 Agent 的既有修改權限仍可用。
	call(agent, "smalltalk_update_profile", map[string]any{"display_name": "唯讀授權測試更名"}, false)
}

func testViewLifecycle(t *testing.T, s *Store) {
	t.Helper()
	token := seedViewAgent(t, s)
	p := &requestAuthContext{ClientID: "view-agent", TokenKind: "agent", CredentialHash: viewHash(token)}
	record, browser, err := s.createViewRequest("isolated")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.authorizeViewToken(browser); ok {
		t.Fatal("未核准就登入")
	}
	if err := s.approveViewRequest(record.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.authorizeViewToken(browser); ok {
		t.Fatal("尚未在原瀏覽器完成登入")
	}
	if _, err := s.pollViewRequest(browser); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.authorizeViewToken(browser); !ok {
		t.Fatal("已核准 session 無效")
	}
	s.mu.Lock()
	s.agentRegistry[p.ClientID].Blocked = true
	s.mu.Unlock()
	if _, ok := s.authorizeViewToken(browser); ok {
		t.Fatal("封鎖仍有效")
	}
	s.mu.Lock()
	s.agentRegistry[p.ClientID].Blocked = false
	s.agentRegistry[p.ClientID].Token = "rotated"
	s.mu.Unlock()
	if _, ok := s.authorizeViewToken(browser); ok {
		t.Fatal("原 TOKEN 更換後仍有效")
	}
	s.mu.Lock()
	s.agentRegistry[p.ClientID].Token = token
	records, err := s.readViewRequestLocked(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := records[record.ID]
	r.ExpiresAt = time.Now().Add(-time.Second)
	err = s.saveViewRequestLocked(records, r)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.authorizeViewToken(browser); ok {
		t.Fatal("到期仍有效")
	}
}
func TestWebViewLifecycle(t *testing.T) { testViewLifecycle(t, NewStore(t.TempDir(), 100, false)) }
