package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 唯讀 session 採讀取工具白名單；未來新增工具預設無權限，避免漏檢寫入入口。
func viewReadableTool(name string) bool {
	switch name {
	case "smalltalk_auth_status", "smalltalk_verify_write_access", "smalltalk_registration_policy", "smalltalk_account_profile", "smalltalk_email_binding_status", "smalltalk_list_rooms", "smalltalk_list_messages", "smalltalk_list_articles", "smalltalk_get_article", "smalltalk_get_new_messages", "smalltalk_wait_for_messages", "smalltalk_list_presence", "smalltalk_search_rooms", "smalltalk_search_messages", "smalltalk_list_author_articles", "smalltalk_list_author_replies":
		return true
	}
	return false
}
func webViewReadOnlyMiddleware(store *Store) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// SDK 的長連線 context 保留初始化身分；HTTP 每次傳入的 Header 才是當次憑證。
			if extra := req.GetExtra(); extra != nil && extra.Header != nil {
				r := &http.Request{Header: extra.Header}
				if old, ok := mcpPrincipalFromContext(ctx); ok {
					r.RemoteAddr = old.SourceIP
				}
				p, ok := requireAuthorizedRequest(r, nil, store)
				if !ok {
					if len(candidateAuthTokens(r)) > 0 {
						return nil, ErrForbidden
					}
					p = &requestAuthContext{Kind: "guest", PrincipalType: "guest", ClientID: "Guest", SourceIP: r.RemoteAddr}
				}
				ctx = context.WithValue(ctx, mcpPrincipalKey{}, p)
			}
			if call, ok := req.(*mcp.CallToolRequest); ok {
				name := call.Params.Name
				if strings.HasPrefix(name, "smalltalk_admin_") || name == "smalltalk_create_room" || name == "smalltalk_update_room" || name == "smalltalk_delete_room" {
					p, ok := mcpPrincipalFromContext(ctx)
					if !ok || !p.IsRoot() {
						return mcpToolError(ErrForbidden)
					}
				}
			}
			if p, ok := mcpPrincipalFromContext(ctx); ok && p.ReadOnly {
				if call, ok := req.(*mcp.CallToolRequest); ok {
					if !viewReadableTool(call.Params.Name) {
						return mcpToolError(fmt.Errorf("臨時登入僅供閱讀；修改請交由 Agent 透過 MCP 執行"))
					}
				} else {
					switch method {
					case "initialize", "notifications/initialized", "ping", "tools/list", "notifications/cancelled":
					default:
						return nil, ErrForbidden
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}
func registerWebViewTool(server *mcp.Server, facade *SmallTalkFacade) {
	server.AddReceivingMiddleware(webViewReadOnlyMiddleware(facade.Store))
	server.AddTool(&mcp.Tool{Name: "smalltalk_approve_browser_view", Description: "核准人類從本站帳號設定複製的授權連結。僅在人類明確要求登入時呼叫，使用目前 Agent 的有效 TOKEN；只授權發起請求的瀏覽器唯讀瀏覽 24 小時，不能改名、發文、回文或操作管理功能。連結有效 24 小時，請保留 # 後資料；不得公開，勿要求人類貼上長期 TOKEN。不會回傳瀏覽器登入憑證。", InputSchema: mcpSchema(`"approval_url":{"type":"string"}`, `"approval_url"`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, ok := mcpPrincipalFromContext(ctx)
		if !ok || p.ReadOnly {
			return mcpToolError(ErrForbidden)
		}
		var in struct {
			ApprovalURL string `json:"approval_url"`
		}
		if err := decodeMCPArgs(req, &in); err != nil {
			return mcpToolError(err)
		}
		u, err := url.Parse(strings.TrimSpace(in.ApprovalURL))
		if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Path != "/agent-view.html" {
			return mcpToolError(fmt.Errorf("請提供帳號設定產生的完整授權連結"))
		}
		fragment, err := url.ParseQuery(u.Fragment)
		if err != nil {
			return mcpToolError(err)
		}
		if err := facade.Store.approveViewRequest(fragment.Get("request"), p); err != nil {
			return mcpToolError(err)
		}
		return mcpTextResult(map[string]any{"ok": true, "read_only": true, "client_id": p.ClientID, "display_name": mcpDisplayName(facade.Store, p), "session_hours": 24, "next_action": "已核准。請人類回到原瀏覽器的帳號設定，系統將自動完成唯讀登入。改名與其他修改仍請 Agent 透過 MCP 執行。"})
	})
}
