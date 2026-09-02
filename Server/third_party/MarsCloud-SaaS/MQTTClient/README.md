# MQTTClient

`MQTTClient` 是 SDK 的 MQTT 客戶端封裝，底層使用 Eclipse Paho，對外提供較接近 Java 舊版 SDK 的介面。

## 主要能力

- 建立 MQTT client
- 設定 callback
- 連線、斷線、重連
- 訂閱與發佈
- 支援 `tcp`、`ssl`、`ws`、`wss`

## 主要型別

- `MQTTClient`
- `MQTTConnectOptions`
- `MQTTCallback`
- `MQTTMessage`

## 常用函式

- `Create() (*MQTTClient, error)`
- `NewMQTTConnectOptions() *MQTTConnectOptions`
- `(_this *MQTTClient) SetCallback(_cb MQTTCallback)`
- `(_this *MQTTClient) Connect(_options *MQTTConnectOptions) error`
- `(_this *MQTTClient) Subscribe(_topic string, _qos byte)`
- `(_this *MQTTClient) Publish(_topic string, _qos byte, _retained bool, _payload string)`
- `(_this *MQTTClient) Disconnect(_quiesce uint)`
- `(_this *MQTTClient) IsConnected() bool`

## 基本範例

```go
client, _ := MQTTClient.Create()
opts := MQTTClient.NewMQTTConnectOptions()
opts.SetServer("tcp://127.0.0.1:1883")
opts.SetClientID("demo-client")
opts.SetUserName("demo")
opts.SetPassword([]byte("demo"))
client.Connect(opts)
```

## 注意事項

- `MarsService` 內部已經幫你處理 topic 與 callback 綁定
- 一般 service 開發若是連 MarsCloud，不建議再自行初始化第二套 MQTT client
