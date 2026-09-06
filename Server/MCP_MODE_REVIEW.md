# MCP 三模式一致性檢查

本機修正日期：2026-09-06；本次修改尚未部署。

正式站唯讀查詢發現：當時為 standard，但 initialize 共用混合模式說明，要求所有請求使用 TOKEN，與 open 的例外衝突；registration_policy 的 open_board_scope 亦漏寫管理員 ID 例外。

已修正：

| 模式 | 初始化與註冊／發文工具說明 | 實際行為 |
| --- | --- | --- |
| open | 開放模式專用說明 | 普通有效既有 ID 可免 TOKEN 讀寫一般看板；管理員 ID 必須本人有效 TOKEN；新註冊沿用 standard |
| standard | 標準模式專用說明 | Email 必填，立即建帳與核發 TOKEN；一般寫入需 TOKEN，Email 確認不補發 TOKEN |
| strict | 嚴格模式專用說明 | Email 驗證完成才建帳與核發 TOKEN；既有帳號仍用有效 TOKEN |

三套說明共用一致的私人功能、角色 Email 門檻、綁定、復原、寄信及憑證保存規則。沒有複製三份業務邏輯或拆成不同 MCP URL。

HTTP 服務預先建立三種模式各自的公開／管理工具服務，初始化時選擇目前模式。既有連線的初始化說明與 tools/list 仍是快照，因此明確要求重新查 smalltalk_registration_policy；此工具回傳即時 mode_instructions，以及 admin_id_requires_matching_token、role_requires_verified_email。重連後取得新模式的初始化與工具說明。

另外修正 Email 驗證工具的 TOKEN 核發描述，以及開放身分讀取看板清單時不可被標成具版主操作權限。免 TOKEN 身分的工具白名單不變，不能取得私訊、帳號憑證或管理權限。

驗證：完整 Go 測試通過；TestMCPThreeModeInstructionsLocalHTTP 經真正 HTTP initialize、tools/list、tools/call，檢查三種不同說明，以及切換模式後既有／新連線取得相同即時政策。既有七帳號開放模式與註冊模式 smoke 一併納入回歸測試。正式檢查僅讀取 initialize 與 registration_policy，未使用帳號憑證、未新增正式內容。

三模式切換、七帳號開放模式與註冊模式的 race smoke 亦通過。
