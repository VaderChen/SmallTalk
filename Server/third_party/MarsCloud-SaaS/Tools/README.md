# Tools

`Tools` 是 SDK 的基礎工具模組，包含日誌、HTTP、網路、系統資訊、Shell、編碼、郵件與例外處理等常用能力。

## 主要能力

- 日誌輸出
- HTTP GET / POST
- 本機 IP / MAC / Machine ID
- Shell 指令同步與非同步執行
- Base64 / SHA1 / Hex
- 郵件寄送
- Process / CPU / Memory 資訊
- 例外保護與 recovery

## 主要檔案

- `Log.go`
- `Tools.go`

## 常用函式

日誌：

- `Tools.Log.Print(...)`
- `Tools.Log.SetDisplayLevel(...)`

HTTP：

- `HttpGet(...)`
- `HttpPost(...)`
- `HttpPostWithHeaders(...)`

系統資訊：

- `GetOSName()`
- `GetLocalIPv4Address()`
- `GetLocalMACAddress(...)`
- `GetMachineID()`
- `GetPID(...)`

流程控制：

- `Sleep(_ms int)`
- `SafeRun(_task func())`
- `EnableUncaughtExceptionHandler(...)`
- `GlobalRecovery()`

Shell：

- `ShellCMDSync(...)`
- `ShellCMDAsync(...)`

## 範例

```go
Tools.Log.SetDisplayLevel(Tools.LL_Info)
Tools.Log.Print(Tools.LL_Info, "Local IP: %s", Tools.GetLocalIPv4Address())
```

```go
resp := Tools.HttpGet("https://www.mars-cloud.com/api/hello", "", 5000)
```

## 注意事項

- `HttpPost` 會自動帶相容的認證 header
- 某些工具函式偏系統層，跨平台行為可能略有差異
- `Tools` 很大，建議業務程式只用必要能力，避免把所有基礎能力都耦合進單一模組
