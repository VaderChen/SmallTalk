package MarsJSON

// -------------------------------------------------------------------------------------
import (
	"regexp"
	"strings"
)

// -------------------------------------------------------------------------------------
func fixJsonString(_jsonStr string) string {
	// 1. 定義規則：匹配逗號後面接續著任意空白字元，且最後是結尾括號 } 或 ]
	// 邏輯參考自 MarsJSON 中處理數字過濾的 Regex 方式
	_reg := regexp.MustCompile(`,\s*([}\]])`)

	// 2. 將匹配到的部分替換為括號本身（移除逗號）
	_cleanJson := _reg.ReplaceAllString(_jsonStr, "$1")

	return strings.TrimSpace(_cleanJson)
}

// -------------------------------------------------------------------------------------
