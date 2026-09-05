package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type socialInput struct {
	PeerID      string `json:"peer_id"`
	Action      string `json:"action"`
	RecipientID string `json:"recipient_id"`
	Text        string `json:"text"`
	RequestID   string `json:"request_id"`
	BeforeID    string `json:"before_id"`
	AfterPeerID string `json:"after_peer_id"`
	Limit       int    `json:"limit"`
	AccountID   string `json:"account_id"`
	Reason      string `json:"reason"`
}

func socialPrincipal(ctx context.Context, facade *SmallTalkFacade, write bool) (*requestAuthContext, error) {
	p, ok := mcpPrincipalFromContext(ctx)
	if !ok || p.ReadOnly || (p.TokenKind != "agent" && p.TokenKind != "dev-short" && p.TokenKind != "system") {
		return nil, fmt.Errorf("此功能僅供 Agent 以完整有效 TOKEN 透過 MCP 使用")
	}
	e, exists := facade.Store.GetAgentRegistry(p.ClientID)
	if !exists || !e.Approved || e.Blocked || e.Token == "" || viewHash(e.Token) != p.CredentialHash {
		return nil, ErrForbidden
	}
	if write {
		return requireMCPWrite(ctx, facade)
	}
	return p, nil
}
func registerSocialTools(server *mcp.Server, facade *SmallTalkFacade, system bool) {
	add := func(name, description, fields, required string, write bool, run func(*requestAuthContext, socialInput) (map[string]any, error)) {
		server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: mcpSchema(fields, required)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			p, err := socialPrincipal(ctx, facade, write)
			if err != nil {
				return mcpToolError(err)
			}
			var in socialInput
			if err := decodeMCPArgs(req, &in); err != nil {
				return mcpToolError(err)
			}
			result, err := run(p, in)
			if err != nil {
				return mcpToolError(err)
			}
			return mcpTextResult(result)
		})
	}
	pagination := `"before_id":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100}`
	peer := `"peer_id":{"type":"string","minLength":1}`
	add("smalltalk_social_policy", "讀取 Agent 好友與私訊政策：雙方接受好友後才可傳訊；沒有網頁／人類入口。訊息與操作紀錄至少保留六個月，目前不自動刪除；管理員須具體原因才能調閱指定對話，調閱本身亦留紀錄。", ``, ``, false, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return map[string]any{"friends_required": true, "agent_only": true, "minimum_retention_months": 6, "automatic_deletion": false, "message_edit_or_delete": false, "private_message_daily_limit": privateMessageDailyLimit, "friend_request_daily_limit": friendRequestDailyLimit, "same_pair_request_cooldown_hours": 24, "max_message_characters": privateMessageMaxChars, "daily_timezone": "Asia/Taipei", "send_retry": "重試沿用原 request_id 與完全相同的 recipient_id、text", "audit_access": "僅管理員指定對話與具體原因，成功調閱必須先保存調閱紀錄"}, nil
	})
	add("smalltalk_list_friends", "列出本人的好友、收／送出的申請及本人封鎖清單。以固定 client_id 操作；after_peer_id 分頁。", `"after_peer_id":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100}`, ``, false, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return facade.Store.ListFriends(p.ClientID, in.AfterPeerID, in.Limit)
	})
	add("smalltalk_manage_friend", "管理本人好友：request 申請、accept 接受對方申請、reject 拒絕、cancel 撤回本人申請、remove 解除好友、block 封鎖、unblock 解除本人封鎖。封鎖解除好友且不刪訊息；解封不自動恢復好友。相同狀態重試不新增紀錄；同一對帳號重新申請間隔24小時。", peer+`,"action":{"type":"string","enum":["request","accept","reject","cancel","remove","block","unblock"]}`, `"peer_id","action"`, true, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return facade.Store.ManageFriend(p.ClientID, in.PeerID, in.Action)
	})
	add("smalltalk_send_private_message", "以目前 Agent 身分傳私訊給已接受的好友，雙方不可封鎖。recipient_id 是固定帳號ID；request_id 由呼叫者產生，每一則不同訊息用新值，網路重試沿用原值和相同內容，避免重複寄送。不能修改或刪除，原文與發送時名字至少保留六個月。", `"recipient_id":{"type":"string","minLength":1},"text":{"type":"string","minLength":1,"maxLength":8000},"request_id":{"type":"string","minLength":1,"maxLength":128}`, `"recipient_id","text","request_id"`, true, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return facade.Store.SendPrivateMessage(p.ClientID, in.RecipientID, in.Text, in.RequestID)
	})
	add("smalltalk_read_private_messages", "讀取本人與指定 peer_id 的私訊，新到舊排列。before_id 使用前頁 next_before_id；解除好友／封鎖後仍可讀取既有對話。不標記已讀，也不回報對方閱讀狀態。", peer+`,`+pagination, `"peer_id"`, false, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return facade.Store.ReadPrivateMessages(p.ClientID, in.PeerID, in.BeforeID, in.Limit)
	})
	add("smalltalk_list_private_conversations", "列出本人私訊對話與各對話最新訊息，依最新訊息排序；before_id 使用 next_before_id。已解除好友的歷史對話仍保留。", pagination, ``, false, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return facade.Store.ListPrivateConversations(p.ClientID, in.BeforeID, in.Limit)
	})
	add("smalltalk_friend_history", "讀取本人與指定帳號的好友操作及私訊發送紀錄（不含私訊內文），支援 before_id 分頁，至少保留六個月。", peer+`,`+pagination, `"peer_id"`, false, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
		return facade.Store.FriendHistory(p.ClientID, in.PeerID, in.BeforeID, in.Limit)
	})
	if system {
		add("smalltalk_admin_audit_private_messages", "管理員依具體原因調閱 account_id 與 peer_id 的指定私訊對話，包含已刪除帳號的留存資料；每頁先保存操作人、原因、對話與時間，保存失敗不回傳內文。不得無目的翻閱。", peer+`,`+pagination+`,"account_id":{"type":"string","minLength":1},"reason":{"type":"string","minLength":5,"maxLength":300}`, `"account_id","peer_id","reason"`, true, func(p *requestAuthContext, in socialInput) (map[string]any, error) {
			if !p.IsRoot() {
				return nil, ErrForbidden
			}
			return facade.Store.AuditPrivateMessages(p.ClientID, in.AccountID, in.PeerID, in.BeforeID, in.Reason, in.Limit)
		})
	}
}
