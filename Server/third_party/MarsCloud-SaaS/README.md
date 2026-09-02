# MarsCloud-SaaS Go SDK

MarsCloud-SaaS SDK for Go，提供 MarsCloud 雲端服務的開發套件。

## 版本

- **Go 版本**: 1.18+

## 安裝

```bash
go get github.com/MarsSemi/MarsCloud-SaaS/SDK/Go
```

## 設定檔重點

SDK 預設使用 `agent.properties` 作為主要設定檔。以下是本版實際需要注意的欄位語意。

### 基本服務設定

```json
{
  "service_name": "Service_A1021B",
  "http_port": 8081,
  "https_port": 8443,
  "mars_cloud_url": "https://test.mars-cloud.com",
  "mars_cloud_account": "test",
  "mars_cloud_password": "test",
  "mars_cloud_proj": "justtest"
}
```

說明：

- `service_name`
  - 服務名稱，會出現在註冊資訊中。
- `http_port`
  - HTTP 監聽埠。
- `https_port`
  - HTTPS 監聽埠。
- `mars_cloud_url`
  - MarsCloud 服務位址。
- `mars_cloud_account`
  - MarsCloud 帳號。
- `mars_cloud_password`
  - MarsCloud 密碼。
- `mars_cloud_proj`
  - 專案代號，會影響登入與 MQTT topic。

### MarsCloud 啟動條件

若 `mars_cloud_url`、`mars_cloud_account`、`mars_cloud_password` 三者任一缺少，`MarsService` 不會登入 MarsCloud，也不會建立雲端 MQTT client，會直接當一般 HTTP/HTTPS server 啟動。

### 本地 MQTT Server

本地 MQTT broker 預設不啟動，只有在 `mqtt_server_enable=true` 時才會開啟。

```json
{
  "mqtt_server_enable": false,
  "mqtt_bind": "",
  "mqtt_tcp_port": 1883,
  "mqtt_ws_port": 1884,
  "mqtt_ssl_port": 8883,
  "mqtt_wss_port": 8884,
  "mqtt_tls_cert": "",
  "mqtt_tls_key": ""
}
```

說明：

- `mqtt_server_enable`
  - 預設 `false`。
  - 設為 `true` 才會啟動內建 broker。
- `mqtt_bind`
  - 綁定 IP，留空代表綁定所有介面。
- `mqtt_tcp_port`
  - MQTT TCP 埠，預設 `1883`。
- `mqtt_ws_port`
  - MQTT WebSocket 埠，預設 `1884`。
- `mqtt_ssl_port`
  - MQTT TLS 埠，預設 `8883`。
- `mqtt_wss_port`
  - MQTT WSS 埠，預設 `8884`。
- `mqtt_tls_cert`
  - TLS 憑證檔。
- `mqtt_tls_key`
  - TLS 私鑰檔。

若 `mqtt_tls_cert` 或 `mqtt_tls_key` 未設定，則只會啟用 `tcp/ws`，`ssl/wss` 會被略過。

### 本地 MQTT 訊息 callback

若有啟用本地 MQTT broker，可用 `SetLocalMQTTMessageCallback` 取得收到的 topic 與 payload：

```go
ms := MarsService.Create("agent.properties", service)
ms.SetLocalMQTTMessageCallback(func(topic string, payload string) {
    Tools.Log.Print(Tools.LL_Info, "Local MQTT: %s => %s", topic, payload)
})
ms.Start()
```

### HTTPS 憑證設定

`ssl_key`、`ssl_key_file`、`ssl_key_password` 的語意如下：

```json
{
  "ssl_key": "",
  "ssl_key_file": "",
  "ssl_key_password": ""
}
```

規則：

- 當 `ssl_key` 是 `.crt` 或 `.pem`
  - `ssl_key` 代表憑證檔
  - `ssl_key_file` 代表對應的 `.key` 私鑰檔
  - `ssl_key_password` 不使用
- 當 `ssl_key` 是 `.p12` 或 `.pfx`
  - `ssl_key` 代表 PKCS#12 憑證包
  - `ssl_key_password` 代表其密碼
  - `ssl_key_file` 不使用

### 相容 JWT 驗證設定

若需要讓新版 server 接受舊版 token，可在 `agent.properties` 額外提供相容金鑰：

```json
{
  "default_aes": "./cert/aes.key",
  "default_rsa_pub": "./cert/rsa.pub",
  "default_rsa_pri": "./cert/rsa.pri",
  "legacy_aes": "./legacy/aes.key",
  "legacy_rsa_pub": "./legacy/rsa.pub",
  "legacy_rsa_pri": "./legacy/rsa.pri",
  "compat_aes": "./compat/aes.key",
  "compat_rsa_pub": "./compat/rsa.pub",
  "compat_rsa_pri": "./compat/rsa.pri"
}
```

SDK 會先驗證目前使用中的 JWT 金鑰，失敗後再 fallback 這些相容金鑰組。

## 模組總覽

| 模組 | 說明 |
|------|------|
| MarsService | 主服務框架，整合 MQTT、HTTP、WebSocket |
| MarsClient | MarsCloud 客戶端，處理認證、訊息收發 |
| MarsJSON | JSON 處理工具，支援 JSONObject 與 JSONArray |
| MQTTClient | MQTT 客戶端，訊息訂閱與發佈 |
| HttpService | HTTP 服務框架，RESTful API 處理 |
| Security | 安全模組，包含 JWT、AES、RSA 加密 |
| Tools | 工具集合，日誌、網路、檔案操作 |
| DataCompress | 資料壓縮，ZIP 加密/解密 |
| AsyncTaskProcessor | 非同步任務處理器 |
| ScriptExecutor | JavaScript 腳本引擎 |

---

## 模組詳解

### MarsService

主服務框架，提供 MarsCloud 服務的完整解決方案。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsService"
```

**主要功能**：
- MQTT 連線管理
- HTTP/HTTPS 伺服器
- 本地 MQTT broker
- WebSocket 支援
- 屬性配置管理
- 訊息處理
- MarsCloud 與舊版 service 相容模式

**基本使用**：
```go
type MyService struct {
    *MarsService.MarsService
    Counter int
}

func (s *MyService) OnMQTTConnected() {
    Tools.Log.Print(Tools.LL_Info, "MQTT connected")
}

func (s *MyService) OnMQTTMessage(topic, payload string) {
    Tools.Log.Print(Tools.LL_Info, "Received: %s", payload)
}

func main() {
    service := &MyService{Counter: 0}
    ms := MarsService.Create("agent.properties", service)
    ms.SetLocalMQTTMessageCallback(func(topic string, payload string) {
        Tools.Log.Print(Tools.LL_Info, "Local MQTT: %s -> %s", topic, payload)
    })
    ms.Start()
}
```

**認證與相容性補充**：

- HTTP 認證同時接受 `Authentication` 與 `Authorization` header。
- 若 header 不是 `Bearer xxx`，純 token 字串也可驗證。
- 也支援 query string 的 `token` 參數。
- JWT 產生採用 `RSA-OAEP + A128GCM`，以對齊舊版 Java service。
- JWT 驗證會同時嘗試 `RSA-OAEP` 與 `RSA-OAEP-256`。

---

### MarsClient

MarsCloud 客戶端，負責與 MarsCloud 伺服器通訊。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsClient"
```

**主要功能**：
- 連線管理
- 訊息發佈/訂閱
- 檔案傳輸（Base64 編碼）
- 屬性同步

---

### MarsJSON

JSON 處理工具，類似 JavaScript 的物件操作風格。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
```

**主要功能**：
- JSONObject 鍵值操作
- JSONArray 陣列操作
- 自動類型轉換

**基本使用**：
```go
// 解析 JSON
obj := MarsJSON.NewJSONObject(`{"name": "test", "value": 123}`)

// 獲取值
name := obj.OptString("name", "default")
value := obj.OptInt("value", 0)

// 設置值
obj.Set("newKey", "newValue")

// 轉為字串
jsonStr := obj.ToString()
```

---

### MQTTClient

MQTT 訊息客戶端，基於 Eclipse Paho MQTT 庫。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MQTTClient"
```

**主要功能**：
- QoS 0/1/2 支援
- 訊息訂閱與發佈
- 保留訊息
- 連線狀態回調
- 支援 `tcp`、`ssl`、`ws`、`wss`

---

### MQTTServer

內建 MQTT broker，採用 `mochi-mqtt/server`。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MQTTServer"
```

**主要功能**：
- 本地嵌入式 MQTT broker
- `tcp`、`ssl`、`ws`、`wss` 四種 listener
- 外部 callback 取得 broker 收到的訊息

**預設埠號**：
- `1883`: MQTT / TCP
- `1884`: MQTT / WebSocket
- `8883`: MQTT / TLS
- `8884`: MQTT / WSS

---

### HttpService

HTTP 服務框架，簡化 RESTful API 開發。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/HttpService"
```

**主要功能**：
- 路由處理
- 靜態檔案服務
- WebSocket 升級
- CORS 支援

若 callback 想明確表示 response 已經自行處理完成，可回傳：

```go
return []byte(HttpService.ResponseHandledMarker)
```

這樣 `HttpAPI` 不會再自動補 `SendResponse(...)`。

**服務 API 自我描述建議**：

新版 Go service 建議至少提供兩支基礎入口：

- `/api/hello`
  - 統一健康檢查
  - 也是最小可測試的通用 API
- `/api/list`
  - 回報目前服務可呼叫的 API 清單
  - 可視為 service 自我描述入口
  - 概念上接近 MCP 風格的 capability / tool listing

建議 `/api/list` 回傳格式：

```json
{
  "service": "authhub",
  "apis": [
    {
      "path": "/api/hello",
      "method": "GET",
      "description": "service hello"
    },
    {
      "path": "/auth/login",
      "method": "POST",
      "description": "issue access token",
      "payload": {
        "usr": "root",
        "pwd": "p@ssw0rd",
        "proj": "demo"
      }
    }
  ]
}
```

欄位說明：

- `path`
  - API 路徑
- `method`
  - `GET` 或 `POST`
- `description`
  - 供 UI 或管理端顯示的說明
- `payload`
  - 可選
  - 若為 `POST` API，可提供建議 request body

這樣的好處是：

- 管理端可動態讀出每個 service 的能力
- 不需要把 API 清單硬編碼在前端
- 測試與整合頁面可在選到 API 後，自動帶入建議 payload

---

### Security

安全相關功能，包含加密與認證。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Security"
```

**主要功能**：
- **JWT**: JSON Web Token 產生與驗證
- **AES**: AES 加密/解密
- **RSA**: RSA 加密/解密
- **UserVerify**: 用戶認證與權限管理

**相容性補充**：

- 為了相容舊版 Java service，RSA JWE 預設使用 `RSA-OAEP`。
- 驗證時會 fallback 舊格式 token 與相容金鑰。
- `UserVerify` 會自動處理舊版 token 驗證需求，不需要每個 API 自己重複寫 fallback。

---

### Tools

工具集合，包含多種常用功能。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
```

**主要功能**：
- **Log**: 日誌系統，支援多級別輸出
- **HTTP**: HTTP 客戶端工具
- **Net**: 網路工具
- **File**: 檔案操作
- **Encode**: Base64 編碼
- **Mail**: SMTP 郵件發送
- **Time**: 時間處理

**日誌級別**：
```go
Tools.Log.SetDisplayLevel(Tools.LL_Debug)   // 調試
Tools.Log.SetDisplayLevel(Tools.LL_Normal)  // 一般
Tools.Log.SetDisplayLevel(Tools.LL_Info)    // 資訊
Tools.Log.SetDisplayLevel(Tools.LL_Warning) // 警告
Tools.Log.SetDisplayLevel(Tools.LL_Error)   // 錯誤
```

---

### DataCompress

資料壓縮工具。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/DataCompress"
```

**主要功能**：
- ZIP 檔案解壓
- 加密 ZIP 支援

Processor

非同步---

### AsyncTask任務處理器。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/AsyncTaskProcessor"
```

**主要功能**：
- 非同步任務排程
- Webhook 回調

---

### ScriptExecutor

JavaScript 腳本引擎，基於 goja。

```go
import "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/ScriptExecutor"
```

**主要功能**：
- 執行 JavaScript 腳本
- 腳本熱重載
- 預編譯腳本

---

## 範例程式

### 基本伺服器

```go
package main

import (
    "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsService"
    "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
    "github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
)

type MyService struct {
    *MarsService.MarsService
}

func (s *MyService) OnMQTTConnected() {
    Tools.Log.Print(Tools.LL_Info, "Connected to MarsCloud")
}

func (s *MyService) OnMQTTMessage(topic, payload string) {
    Tools.Log.Print(Tools.LL_Info, "Message: %s -> %s", topic, payload)
}

func (s *MyService) OnPropertyChange(prop *MarsJSON.JSONObject) {
    Tools.Log.Print(Tools.LL_Info, "Property changed")
}

func main() {
    service := &MyService{}
    ms := MarsService.Create("agent.properties", service)
    ms.Start()
}
```

---

## 依賴套件

SDK 依賴以下第三方套件：

- `github.com/eclipse/paho.mqtt.golang` - MQTT 客戶端
- `github.com/gorilla/websocket` - WebSocket
- `github.com/dop251/goja` - JavaScript 引擎
- `github.com/lestrrat-go/jwx` - JWT/JWE/JWS
- `github.com/alexmullins/zip` - 加密 ZIP

完整依賴請參考 [go.mod](https://github.com/MarsSemi/MarsCloud-SaaS/blob/main/SDK/Go/go.mod)。

---

## 授權

本 SDK 遵循 MarsCloud 服務條款。

---

## 相關連結

- [MarsCloud 官網](https://mars-cloud.com)
- [GitHub 倉庫](https://github.com/MarsSemi/MarsCloud-SaaS)
