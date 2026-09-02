# HttpService

`HttpService` 是 SDK 的 HTTP/HTTPS 服務模組，負責靜態檔案、RESTful API、系統設定 API 與基本安全檢查。

## 主要能力

- 啟動 HTTP / HTTPS server
- 掛載 RESTful API callback
- 提供系統設定 API
- 支援靜態檔案目錄
- 支援 `.crt/.pem + .key` 與 `.p12/.pfx` 憑證載入
- 支援 HTTP token 認證 fallback

## 主要型別

- `HttpService`
- `HttpAPI_Callback`
- `HttpAPI_System`

## 主要函式

- `Create(_http_port, _https_port int, _ssl_cert, _ssl_key_file, _ssl_pwd string) *HttpService`
- `(_this *HttpService) AddRestfulAPI(_uri string, _callback HttpAPI_Callback)`
- `(_this *HttpService) SetRootPath(_path string)`
- `(_this *HttpService) SetDefaultHTML(_default_html string)`
- `(_this *HttpService) SetDefaultCacheControl(_control string)`
- `(_this *HttpService) Run()`
- `(_this *HttpService) Close() bool`
- `SendResponse(_w http.ResponseWriter, _no int, _contentType string, _content []byte)`
- `ResponseHandledMarker`

## Callback 介面

```go
type HttpAPI_Callback interface {
    Process(http.ResponseWriter, *http.Request, *MarsJSON.JSONObject, []string, *MarsJSON.JSONObject, string) []byte
}
```

參數說明：

- `http.ResponseWriter`
  - 可自行寫出 response
- `*http.Request`
  - 原始請求
- `*MarsJSON.JSONObject`
  - 驗證成功後的 JWT payload；未登入時可能為 `nil`
- `[]string`
  - 切分後的 path
- `*MarsJSON.JSONObject`
  - query params
- `string`
  - request body

## 基本範例

```go
type HelloAPI struct{}

func (_h *HelloAPI) Process(w http.ResponseWriter, r *http.Request, jwt *MarsJSON.JSONObject, path []string, params *MarsJSON.JSONObject, body string) []byte {
    return []byte(`{"ok":true}`)
}

svc := HttpService.Create(8081, 8443, "", "", "")
svc.AddRestfulAPI("/api", &HelloAPI{})
svc.SetRootPath("./website")
svc.Run()
```

## 認證行為

`HttpAPI` 目前接受以下 token 來源：

- `Authentication` header
- `Authorization` header
- query string 的 `token`

若 callback 已經自己輸出 response，外層不會再次補 `WriteHeader`，避免出現 `superfluous response.WriteHeader`。

若 callback 是自己完整處理 response，但沒有直接寫入 `ResponseWriter`，可以回傳：

```go
return []byte(HttpService.ResponseHandledMarker)
```

這會告訴 `HttpAPI`：

- 不要再自動包 `SendResponse(...)`
- 不要再補 `404 Not Found`

適合用在以下情境：

- callback 已經自行決定回應流程
- callback 走的是特殊串流或外部代理流程
- callback 想明確表示「這個 request 已處理完成」

## HTTPS 憑證規則

- `ssl_key` 為 `.crt` / `.pem`
  - `ssl_key_file` 必須提供對應 `.key`
- `ssl_key` 為 `.p12` / `.pfx`
  - `ssl_key_password` 代表 p12 密碼

## 內建系統 API

`HttpAPI_System` 主要提供：

- 讀取目前設定
- 更新設定並回寫

通常由 `MarsService` 自動掛載到 `/system`
