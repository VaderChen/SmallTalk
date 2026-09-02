package HttpService

import (
	"net/http"
	"strings"
)

// -------------------------------------------------------------------------------------
// SendResponse 靜態工具函式，發送 HTTP 回應
func SendResponse(_w http.ResponseWriter, _no int, _contentType string, _content []byte) {
	if _w == nil {
		return
	}

	if _contentType == "" {
		_contentType = "text/html; charset=UTF-8"
	}

	_w.Header().Set("Content-Type", _contentType)
	_w.WriteHeader(_no)

	if _content != nil {
		_w.Write(_content)
	}
}

// -------------------------------------------------------------------------------------
// checkURI 檢查請求 URI 是否安全
func checkURI(_w http.ResponseWriter, _uri string) bool {

	if strings.Contains(_uri, "..") {
		http.Error(_w, "Not Acceptable", http.StatusNotAcceptable)
		return false
	}

	return true
}

// -------------------------------------------------------------------------------------
