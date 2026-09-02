# DataCompress

`DataCompress` 提供 ZIP 壓縮與解壓功能，支援密碼保護。

## 主要用途

- 將記憶體中的位元組資料壓縮成 ZIP
- 將 ZIP 檔案或 ZIP bytes 解壓回原始資料
- 供 `MarsClient` 資料壓縮與相容流程使用

## 主要函式

- `UnZip(_fn string, _pwd string) []byte`
- `UnZipBytes(_src []byte, _pwd string) []byte`
- `Zip(_data []byte, _pwd string, _compressLevel int) []byte`
- `ZipDefault(_data []byte, _pwd string) []byte`

## 範例

```go
src := []byte("hello zip")
zipData := DataCompress.ZipDefault(src, "123456")
plain := DataCompress.UnZipBytes(zipData, "123456")
```

## 注意事項

- 回傳值是單一檔案內容的 bytes，不是完整檔案樹結構
- 若密碼錯誤或資料損毀，通常回傳空資料
- 適合處理 SDK 內部交換資料，不適合當成完整封存格式管理工具
