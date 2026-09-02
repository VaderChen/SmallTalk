# MarsService

`MarsService` 是整個 SDK 的 service 框架核心，整合 HTTP/HTTPS、MarsCloud 登入、MQTT client、本地 MQTT broker、設定檔管理與 service lifecycle。

## 主要能力

- 根據 `agent.properties` 建立 service
- 啟動 HTTP / HTTPS server
- 連線 MarsCloud 並建立 MQTT client
- 註冊 service 與 properties
- 管理自動重啟、GC、關機清理
- 在需要時啟動本地 MQTT broker

## 主要型別

- `IMarsService`
- `MarsService`

## `IMarsService` 介面

```go
type IMarsService interface {
    OnMQTTConnected()
    OnMQTTMessage(_topic string, _payload string)
    OnMQTTConnectionLost(_err error)
    OnPropertyChange(*MarsJSON.JSONObject)
    BeforeServiceStop()
    Process()
}
```

## 常用函式

- `Create(_propertyFileName string, _impl IMarsService) *MarsService`
- `(_this *MarsService) Start()`
- `(_this *MarsService) StopService() bool`
- `(_this *MarsService) RestartService()`
- `(_this *MarsService) ShutdownService()`
- `(_this *MarsService) AddRestfulAPI(_uri string, _callback HttpService.HttpAPI_Callback)`
- `(_this *MarsService) RegistryServerInfo(_version string, _type string, _isOnline bool)`
- `(_this *MarsService) SetLocalMQTTMessageCallback(_callback MQTTServer.MessageCallback)`
- `(_this *MarsService) SendResponse(...)`

## 啟動流程摘要

1. 讀取 `agent.properties`
2. 偵測同名舊實例並 `kill`（取代 PID 檔機制）
3. 檢查並清除佔用 `http_port` 的進程
4. 初始化 `HttpService`
5. 視設定決定是否啟本地 MQTT broker
6. 若 `mars_cloud_url/account/password` 完整，登入 MarsCloud
7. 建立 MQTT client、AsyncTaskProcessor、執行 service registry
8. 啟動 HTTP/HTTPS

## 一般範例

```go
type MyService struct {
    *MarsService.MarsService
}

func (s *MyService) OnMQTTConnected() {}
func (s *MyService) OnMQTTMessage(topic, payload string) {}
func (s *MyService) OnMQTTConnectionLost(err error) {}
func (s *MyService) OnPropertyChange(prop *MarsJSON.JSONObject) {}
func (s *MyService) BeforeServiceStop() {}
func (s *MyService) Process() {}

func main() {
    svc := &MyService{}
    ms := MarsService.Create("agent.properties", svc)
    ms.SetLocalMQTTMessageCallback(func(topic, payload string) {})
    ms.RegistryServerInfo("1.0.0", "pack", true)
    ms.Start()
    select {}
}
```

## 關鍵設定

- `mars_cloud_url`
- `mars_cloud_account`
- `mars_cloud_password`
- `mars_cloud_proj`
- `mqtt_server_enable`
- `http_port`
- `https_port`
- `ssl_key`
- `ssl_key_file`
- `ssl_key_password`
- `restart_time`：定時自動重啟時間清單，例如 `["06:00", "14:30"]`
- `restart_timezone`：`restart_time` 比對所用時區（IANA 名稱，如 `Asia/Taipei`、`UTC`）；未設定或解析失敗時退回 `time.Local`

## 注意事項

- `mqtt_server_enable` 預設為 `false`
- 缺少任一 MarsCloud 登入欄位時，只會當一般 server 啟動
- `OnMQTTMessage` 是雲端 MQTT client 的回調，本地 broker 則用 `SetLocalMQTTMessageCallback`
- 啟動時會自動偵測並 `kill` 同執行檔名稱（不同 PID）的舊實例，不再依賴外部 PID 檔；舊版 `run_bg.sh` 寫入 `AgenticService.pid` 的步驟可省略
- 啟動時會 log 出當前 `Restart Timezone`，遠端容器若 `/etc/localtime` 缺失而 fallback `UTC` 可立即看出
- 內建 `RestartService()` 會 fork 新 process 後 `os.Exit(0)`，目的是維持服務持續運作

## 部署注意：與 systemd 的相容性

`MarsService` 內建 `RestartService()` / 自動定時重啟採用「fork 新 process → 舊 process `os.Exit(0)`」模式，這對 `systemd` 而言會被視為**服務正常結束**，導致：

- `Type=simple`（預設）下，systemd 認定服務已退出，不會接管子 process，孤兒 process 會被收養給 PID 1；服務狀態變 `inactive (dead)` 而非 `active (running)`
- 沒設 `Restart=always` 時 systemd 不會重新拉起
- 設了 `Restart=always` 但搭配內建 `RestartService()`，會出現雙重重啟邏輯互相干擾

**建議擇一使用：**

1. **完全交給 systemd 管理（推薦）**
   - 在 `agent.properties` 移除 `restart_time`、避免呼叫 `RestartService()`
   - unit file 設 `Restart=always`、`RestartSec=3`，由 systemd 重啟
   - 收到 `reboot` MQTT 命令時改以 `os.Exit(非 0)` 結束，讓 systemd 接手

2. **使用內建重啟（適合非 systemd 環境，例：容器內以 `run_bg.sh` 啟動、Windows）**
   - unit file 改用 `Type=forking`，並讓 process 自己處理 daemonize
   - 或繼續用 `nohup` / 啟動腳本管理，不交給 systemd

如果一定要在 systemd 下保留內建重啟邏輯，把 unit file 設成 `Type=forking` 並提供 `PIDFile=`，但這樣與本服務「啟動時自動 kill 同名舊實例」的設計會有時序競爭，仍**不建議混用**。
