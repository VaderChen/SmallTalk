# MQTTServer

`MQTTServer` 是內建的本地 MQTT broker 模組，底層採用 `mochi-mqtt/server`。

## 主要能力

- 嵌入式本地 broker
- 支援 `tcp`、`ssl`、`ws`、`wss`
- 可註冊外部 callback，取得收到的 MQTT 訊息

## 主要型別

- `Config`
- `MQTTServer`
- `MessageCallback`

## `Config` 欄位

- `Host`
- `TCPPort`
- `WSPort`
- `SSLPort`
- `WSSPort`
- `CertFile`
- `KeyFile`
- `OnMessage`

## 常用函式

- `Create(_config Config) *MQTTServer`
- `(_this *MQTTServer) Start() error`
- `(_this *MQTTServer) Close() error`

## 基本範例

```go
server := MQTTServer.Create(MQTTServer.Config{
    TCPPort: 1883,
    WSPort:  1884,
    SSLPort: 8883,
    WSSPort: 8884,
    OnMessage: func(topic string, payload string) {
        fmt.Println(topic, payload)
    },
})

if err := server.Start(); err != nil {
    panic(err)
}
defer server.Close()
```

## 預設埠號建議

- `1883`: MQTT / TCP
- `1884`: MQTT / WebSocket
- `8883`: MQTT / TLS
- `8884`: MQTT / WSS

## 注意事項

- 只有在 `mqtt_server_enable=true` 時，`MarsService` 才會啟動它
- 若未設定 `CertFile` / `KeyFile`，則 `ssl/wss` 會被略過
