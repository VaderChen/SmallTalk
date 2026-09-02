# AsyncTaskProcessor

`AsyncTaskProcessor` 負責接收來自 MQTT 的非同步任務訊息，並透過 webhook 將結果回推到指定 HTTP API。

## 主要用途

- 接收 service 型別的 MQTT 任務訊息
- 解析 payload 內的 API 請求
- 將任務轉送到本地 webhook
- 把處理結果再送回 MarsCloud

## 主要型別

- `AsyncTaskProcessor`

## 主要函式

- `Create(_client *MarsClient.MarsClient, _webHook string) *AsyncTaskProcessor`
- `(_this *AsyncTaskProcessor) OnMQTTMessage(_topic string, _payload string)`
- `(_this *AsyncTaskProcessor) ProcessAPI(_payload string)`

## 使用情境

這個模組通常不需要手動建立，`MarsService` 在完成雲端登入與 MQTT 初始化後會自動建立：

```go
ms := MarsService.Create("agent.properties", service)
ms.Start()
```

## 注意事項

- 依賴 `MarsClient` 與可用的 `webHook`
- 若未登入 MarsCloud，通常不會建立 `AsyncTaskProcessor`
- 這個模組偏內部流程控制，不是一般業務 API 的主要入口
