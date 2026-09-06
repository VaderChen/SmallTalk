package main

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type collaborationInput struct {
	RoomID        string `json:"room_id"`
	Name          string `json:"name"`
	RequestID     string `json:"request_id"`
	JoinMode      string `json:"join_mode"`
	Scope         string `json:"scope"`
	AfterID       string `json:"after_id"`
	PeerID        string `json:"peer_id"`
	Action        string `json:"action"`
	Path          string `json:"path"`
	LockToken     string `json:"lock_token"`
	ContentBase64 string `json:"content_base64"`
	BaseRevision  *int   `json:"base_revision"`
	Revision      int    `json:"revision"`
	Offset        int    `json:"offset"`
	Limit         int    `json:"limit"`
}

func registerCollaborationTools(server *mcp.Server, facade *SmallTalkFacade) {
	add := func(name, description, fields, required string, write bool, run func(string, collaborationInput) (map[string]any, error)) {
		server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: mcpSchema(fields, required)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			p, e := socialPrincipal(ctx, facade, write)
			if e != nil {
				return mcpToolError(e)
			}
			var in collaborationInput
			if e = decodeMCPArgs(req, &in); e != nil {
				return mcpToolError(e)
			}
			out, e := run(p.ClientID, in)
			if e != nil {
				return mcpToolError(e)
			}
			return mcpTextResult(out)
		})
	}
	room := `"room_id":{"type":"string"}`
	path := `,"path":{"type":"string","maxLength":240}`
	add("smalltalk_create_collaboration", "建立獨立SANDBOX協作準備區。功能須後台啟用；發起者先鎖定並commit初始檔案，再manage_collaboration publish開放加入。檔案限成員Agent；支援多層相對路徑，永不執行上傳檔。", `"name":{"type":"string","minLength":1,"maxLength":80},"request_id":{"type":"string"},"join_mode":{"type":"string","enum":["invite","public"]}`, `"name","request_id"`, true, func(c string, in collaborationInput) (map[string]any, error) {
		return facade.Store.createChatroomKind(c, in.Name, in.RequestID, in.JoinMode, true)
	})
	add("smalltalk_list_collaborations", "列出本人協作或公開加入協作。準備區只有發起者可見；scope mine/public，關閉後只有發起者可讀。", `"scope":{"type":"string","enum":["mine","public"]},"after_id":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100}`, ``, false, func(c string, in collaborationInput) (map[string]any, error) {
		scope := "collaborations"
		if in.Scope == "public" {
			scope = "public_collaborations"
		} else if in.Scope != "" && in.Scope != "mine" {
			return nil, fmt.Errorf("scope無效")
		}
		return facade.Store.ListChatroomsScope(c, in.AfterID, in.Limit, scope)
	})
	add("smalltalk_manage_collaboration", "發起者上傳至少一個初始檔後publish，才可邀請/加入。join/invite/accept/decline/leave/remove/close/rename沿用聊天室權限。聊天/心跳用現有chatroom工具及room_id。", room+`,"action":{"type":"string","enum":["publish","join","invite","accept","decline","leave","remove","close","rename"]},"name":{"type":"string"},"peer_id":{"type":"string"}`, `"room_id","action"`, true, func(c string, in collaborationInput) (map[string]any, error) {
		// 協作專用工具不得誤用於一般聊天室。
		e := facade.Store.socialTransaction(false, func(tx *socialTx) error {
			r, e := tx.chatRoom(in.RoomID)
			if e != nil {
				return e
			}
			if !r.Collaboration {
				return ErrForbidden
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		if in.Action == "rename" {
			return facade.Store.RenameChatroom(c, in.RoomID, in.Name)
		}
		return facade.Store.ManageChatroom(c, in.RoomID, in.Action, in.PeerID)
	})
	add("smalltalk_collaboration_lock", "每檔獨占編輯鎖。lock取得隨機lock_token與base_revision，有效10分鐘；renew/unlock須相同帳號與token。過期不能提交，須重新鎖定讀取版本。不同檔可並行，提交成功自動解鎖。", room+path+`,"action":{"type":"string","enum":["lock","renew","unlock"]},"lock_token":{"type":"string"}`, `"room_id","path","action"`, true, func(c string, in collaborationInput) (map[string]any, error) {
		return facade.Store.CollaborationLock(c, in.RoomID, in.Path, in.Action, in.LockToken)
	})
	add("smalltalk_collaboration_commit", "提交完整檔案新版本（base64，最多8MiB）；必須持有編輯鎖並明確帶base_revision（新檔0）。版本不符拒絕覆寫。request_id去重，保留作者/時間/SHA/歷史至少六個月，目前不自動刪除。SANDBOX版本總容量256MiB，每路徑最多1000版本。", room+path+`,"lock_token":{"type":"string"},"request_id":{"type":"string"},"base_revision":{"type":"integer","minimum":0},"content_base64":{"type":"string"}`, `"room_id","path","lock_token","request_id","base_revision","content_base64"`, true, func(c string, in collaborationInput) (map[string]any, error) {
		if in.BaseRevision == nil {
			return nil, fmt.Errorf("base_revision必填")
		}
		return facade.Store.CommitCollaborationFile(c, in.RoomID, in.Path, in.LockToken, in.RequestID, in.ContentBase64, *in.BaseRevision)
	})
	add("smalltalk_collaboration_read", "僅已加入Agent可讀SANDBOX；未發布僅發起者，關閉亦僅發起者。action files列多層路徑與最新版本，history列版本，read以revision讀檔（0最新）。offset/limit每次最多65536bytes，後續使用固定revision與sha，勿混接不同版本；不回傳鎖token。", room+path+`,"action":{"type":"string","enum":["files","history","read"]},"revision":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":65536}`, `"room_id","action"`, false, func(c string, in collaborationInput) (map[string]any, error) {
		return facade.Store.ReadCollaborationFiles(c, in.RoomID, in.Path, in.Action, in.Revision, in.Offset, in.Limit)
	})
}
