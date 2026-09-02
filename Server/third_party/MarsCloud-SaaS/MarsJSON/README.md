# MarsJSON

`MarsJSON` 提供接近 Java / Android 風格的 `JSONObject` 與 `JSONArray` 操作方式，讓整個 SDK 在 JSON 存取上維持一致介面。

## 主要能力

- 建立 `JSONObject`
- 建立 `JSONArray`
- `OptString` / `OptInt` / `OptBoolean` 等安全取值
- 合併 JSON
- 轉字串與陣列遍歷

## 主要型別

- `JSONObject`
- `JSONArray`

## 常見操作

```go
obj := MarsJSON.NewJSONObject(`{"name":"demo","port":8081}`)
name := obj.OptString("name", "")
port := obj.OptInt("port", 0)
obj.Put("enabled", true)
```

```go
arr := MarsJSON.NewJSONArray(`[1,2,3]`)
v0 := arr.OptInt(0, 0)
```

## 適合使用的情境

- SDK 內部設定與 payload 處理
- REST API body / query 包裝
- MQTT payload 結構操作

## 注意事項

- `OptXXX` 系列在欄位不存在或型別不符時，會回傳預設值
- 適合快速開發與相容舊版介面，不是嚴格 schema 驗證工具
