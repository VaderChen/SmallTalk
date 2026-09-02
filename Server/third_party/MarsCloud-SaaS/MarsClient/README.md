# MarsClient

`MarsClient` 是連接 MarsCloud 的核心客戶端，負責登入、金鑰同步、服務註冊、資料寫入與查詢。

## 主要能力

- 以帳密、token 或 key 登入
- 自動維護伺服器 URL 清單
- 取得 AES / RSA 安全金鑰
- 註冊 service / properties / device
- 寫入事件與資料
- 查詢歷史資料與最新資料
- 下載 OTA 檔案

## 主要型別

- `MarsClient`
- `AsyncTaskCallback`

## 常用函式

登入與連線：

- `Create() *MarsClient`
- `Login(_url, _account, _pass string) bool`
- `LoginWithProj(_url, _account, _pass, _proj string) bool`
- `LoginByToken(_url, _token string) bool`
- `LoginByKey(_url, _key string) bool`
- `ReLogin() bool`
- `GetServerURL() string`

安全與註冊：

- `ResetSecurityKey() bool`
- `RegistryService(_info string, _resetKey bool) bool`
- `RegistryServiceProperties(_id string, _prop string) bool`
- `RegistryDevice(_root *MarsJSON.JSONObject) bool`

資料操作：

- `PutData(...) bool`
- `PutDataAdv(...) bool`
- `PutEvent(...) bool`
- `GetDataByTime(...) *MarsJSON.JSONObject`
- `GetLastData(...) *MarsJSON.JSONObject`
- `DeleteData(...) bool`

## 基本範例

```go
client := MarsClient.Create()
if !client.LoginWithProj("https://test.mars-cloud.com", "test", "test", "demo") {
    panic("login fail")
}

client.ResetSecurityKey()
```

## 相容性說明

- HTTP 呼叫內部會帶 `Authentication` / `Authorization` header
- 金鑰同步流程對齊舊版控制面鏈路：
  - `login`
  - `get_security_key`
  - `registry`
  - MQTT connect

## 注意事項

- `CallAPI()` 的 `_timeout` 目前存在，但部分內部呼叫仍沿用預設逾時語意
- 若要跑完整 service 啟動流程，通常由 `MarsService` 管理，不建議業務層自行重組整個 lifecycle
