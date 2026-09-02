package HttpService

// -------------------------------------------------------------------------------------
import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Security"
)

const ResponseHandledMarker = "#response_handled#"

// -------------------------------------------------------------------------------------
// HttpAPI Callback 基礎類別
// -------------------------------------------------------------------------------------
type HttpAPI_Callback interface {
	Process(http.ResponseWriter, *http.Request, *MarsJSON.JSONObject, []string, *MarsJSON.JSONObject, string) []byte
}

// -------------------------------------------------------------------------------------
type HttpAPI struct {
	resfulAPI string
	callBack  HttpAPI_Callback
}

// -------------------------------------------------------------------------------------
type trackedResponseWriter struct {
	http.ResponseWriter
	written bool
}

// -------------------------------------------------------------------------------------
func (_w *trackedResponseWriter) WriteHeader(_statusCode int) {
	_w.written = true
	_w.ResponseWriter.WriteHeader(_statusCode)
}

// -------------------------------------------------------------------------------------
func (_w *trackedResponseWriter) Write(_data []byte) (int, error) {
	_w.written = true
	return _w.ResponseWriter.Write(_data)
}

// -------------------------------------------------------------------------------------
func (_h *HttpAPI) servHTTP(_w http.ResponseWriter, _r *http.Request) {

	_uriOrg, _ := url.PathUnescape(_r.RequestURI)

	if checkURI(_w, _uriOrg) == false {
		return
	}

	if _h.callBack == nil {
		http.Error(_w, "Not Found", http.StatusNotFound)
		return
	}

	var _jwt *MarsJSON.JSONObject

	_jwt = nil
	_uriOrg = strings.Replace(_uriOrg, _h.resfulAPI+"/", "", 1)
	_items := strings.Split(_uriOrg, "/")
	_auth := tokenFromRequest(_r)

	if len(_auth) > 0 {
		_jwt = Security.VerifyToken(_auth, "", _r.RemoteAddr)
	}

	// 提取參數
	_params := MarsJSON.NewJSONObject(nil)
	_query := _r.URL.Query()
	for _key, _vals := range _query {
		if len(_vals) > 0 {
			_params.Put(_key, _vals[0])
		}
	}

	// 提取 Body
	_body := ""
	if _r.Body != nil {
		_bytes, _err := io.ReadAll(_r.Body)
		if _err == nil {
			_body = string(_bytes)
		}
	}

	_trackedWriter := &trackedResponseWriter{ResponseWriter: _w}
	_resp := _h.callBack.Process(_trackedWriter, _r, _jwt, _items, _params, _body)

	if _trackedWriter.written {
		return
	}

	if string(_resp) == ResponseHandledMarker {
		return
	}

	if _resp != nil {
		SendResponse(_trackedWriter, http.StatusOK, "application/json; charset=UTF-8", _resp)
		return
	}

	http.Error(_trackedWriter, "Not Found", http.StatusNotFound)
}

// -------------------------------------------------------------------------------------
func CreateHttpAPI(_impl HttpAPI_Callback) *HttpAPI {
	_httpAPI := &HttpAPI{
		callBack: _impl,
	}

	return _httpAPI
}

// ------------------------------------------------------------------------------------
func tokenFromRequest(_r *http.Request) string {
	_auth := strings.TrimSpace(_r.Header.Get("Authentication"))
	if _auth == "" {
		_auth = strings.TrimSpace(_r.Header.Get("Authorization"))
	}
	if _auth != "" {
		return _auth
	}

	return strings.TrimSpace(_r.URL.Query().Get("token"))
}

// ------------------------------------------------------------------------------------
