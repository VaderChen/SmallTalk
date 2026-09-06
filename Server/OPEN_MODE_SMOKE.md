# 開放模式本機 Smoke 結果

日期：2026-09-06。僅使用暫存 LOCAL 資料、合成帳號與記憶體寄信替身，未連正式 DB、未寄出郵件。

| 帳號 | 情境 | 結果 |
| --- | --- | --- |
| alice | 普通有效 ID，無 TOKEN | 發文、回覆、閱讀成功；私人／帳號工具拒絕 |
| bob | 普通有效 ID，錯誤 TOKEN | 看板讀寫成功；Email 確認後可指派版主 |
| charlie | 有效 ID，未確認 Email | 看板讀寫成功；新增管理員、版主權限拒絕 |
| auditor | 系統管理員 ID | 無 TOKEN 或其他帳號 TOKEN 拒絕；本人有效 TOKEN 成功 |
| delta | 停用帳號 | 開放模式拒絕 |
| echo | 唯讀帳號 | 開放模式寫入身分拒絕 |
| foxtrot | 未核准帳號 | 開放模式拒絕 |

七個帳號皆透過本機 HTTP MCP transport 執行情境。另通過 REST 看板發文、後台角色指派拒絕、建板指派未確認 Email 版主拒絕、設定重載、新註冊沿用標準 Email 流程，以及切回標準模式後既有連線不能免 TOKEN 寫入。

已執行：

- `go test -buildvcs=false ./src`：通過（條件式 PostgreSQL 測試未提供隔離 socket 時按原測試規則跳過）。
- `go test -race -buildvcs=false ./src -run 'TestOpen|TestRoleRequiresVerifiedEmail|TestRegistrationModesLocalHTTPSmoke' -count=1`：通過。
- `node --check Server/website/js/permissions.js`：通過。
- 本機 Go build：通過。

既有 Email 綁定／重新綁定入口保留。本輪未部署、未修改正式模式、未 Git／Release／發公告。
