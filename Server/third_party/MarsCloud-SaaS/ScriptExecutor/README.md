# ScriptExecutor

`ScriptExecutor` 提供 JavaScript 執行能力，底層使用 `goja`，可用來做腳本化規則、轉換與快速測試。

## 主要能力

- 執行 JavaScript 字串
- 載入函式集合
- 重新載入腳本
- 呼叫腳本函式
- 暴露部分內建 helper

## 主要型別

- JS 執行器型別由 `CreateJSExecutor()` 建立

## 常用流程

1. 建立執行器
2. 執行一段腳本
3. 或先載入函式，再用 `Call` 呼叫

## 範例

```go
executor := ScriptExecutor.CreateJSExecutor()
result := executor.Execute(`1 + 2`)
fmt.Println(result)
```

```go
id := executor.ReloadFromScript(-1, `
function add(a, b) { return a + b; }
`, true)

sum := executor.Call(id, "add", 10, 20)
```

## 注意事項

- 適合腳本化業務規則與工具用途
- 不建議把長時間阻塞或高風險程式碼直接放在腳本層
