# Security

`Security` 模組提供 JWT、AES、RSA 與使用者 token 驗證相關功能，是整個 SDK 的安全基礎。

## 主要能力

- 產生與解密 JWE token
- 載入 AES / RSA 金鑰
- 驗證 HTTP token
- 舊版 token 與舊金鑰 fallback

## 主要型別

- `JWTProcessor`
- `UserGroup`

## 全域物件

- `Security.JWT`

## 常用函式

JWT：

- `(_this *JWTProcessor) LoadRSAKey(_pubKey []byte, _priKey []byte) bool`
- `(_this *JWTProcessor) LoadRSAKeyFromFile(_pubPath string, _priPath string) bool`
- `(_this *JWTProcessor) LoadAESKey(_key []byte) bool`
- `(_this *JWTProcessor) LoadAESKeyFromFile(_path string) bool`
- `(_this *JWTProcessor) CreateToken(_method string, _root map[string]interface{}) string`
- `(_this *JWTProcessor) DecryptToken(_tokenStr string, _ignoreExp bool) *MarsJSON.JSONObject`

User verify：

- `VerifyToken(_auth_string string, _group UserGroup, _ipadd_from string) *MarsJSON.JSONObject`
- `DecryptToken(_auth_string string, _ignore_timetolive bool) *MarsJSON.JSONObject`

## 相容性重點

- RSA token 建立採用 `RSA-OAEP + A128GCM`
- 解密時會同時嘗試：
  - `RSA-OAEP`
  - `RSA-OAEP-256`
- `UserVerify` 會自動 fallback 相容金鑰：
  - `default_*`
  - `legacy_*`
  - `compat_*`

## 基本範例

```go
Security.JWT.LoadRSAKey(nil, nil)
Security.JWT.LoadAESKey(nil)

token := Security.JWT.CreateToken(Security.TM_AES.Value(), map[string]interface{}{
    "iss": "tester",
    "exp": time.Now().Add(time.Hour).Unix(),
})

claims := Security.JWT.DecryptToken(token, false)
```

## 注意事項

- `exp` 以秒為單位
- `VerifyToken` 會在失敗時引入短暫 delay，避免暴力測試
