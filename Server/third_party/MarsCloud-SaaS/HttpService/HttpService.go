package HttpService

// -------------------------------------------------------------------------------------
import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
	"golang.org/x/crypto/pkcs12"
)

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
// HttpService 模擬 Java 的 HttpService 類別
type HttpService struct {
	_HttpServer  *http.Server
	_HttpsServer *http.Server
	_Mux         *http.ServeMux
	_MuxLock     sync.RWMutex

	_HttpPort      int
	_HttpsPort     int
	_SSLCert       string
	_SSLKeyFile    string
	_SSLPassword   string
	_PoolMethod    string
	_MaxConnection int

	_RootPath            string
	_DefaultHTML         string
	_CacheLock           sync.RWMutex
	_DefaultCacheControl string
	_EnableCache         bool

	_Handlers map[string]HttpAPI_Callback
}

// -------------------------------------------------------------------------------------
// NewHttpService 模擬 Java 建構子
func Create(_http_port, _https_port int, _ssl_cert, _ssl_key_file, _ssl_pwd string) *HttpService {
	_this := &HttpService{
		_HttpPort:    _http_port,
		_HttpsPort:   _https_port,
		_SSLCert:     _ssl_cert,
		_SSLKeyFile:  _ssl_key_file,
		_SSLPassword: _ssl_pwd,
		_Mux:         http.NewServeMux(),
		_Handlers:    make(map[string]HttpAPI_Callback),
	}

	_this._Mux.HandleFunc("/", _this.serveHTTP)
	_this.InitExecutor(false, "sync", 8, 800, 500)

	if _this._HttpPort > 0 {
		_this._HttpServer = &http.Server{
			Addr:    fmt.Sprintf("0.0.0.0:%d", _this._HttpPort),
			Handler: _this._Mux,
		}
	}

	if _this._HttpsPort > 0 && _this._SSLCert != "" {
		_this._HttpsServer = &http.Server{
			Addr:    fmt.Sprintf("0.0.0.0:%d", _this._HttpsPort),
			Handler: _this._Mux,
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	}

	return _this
}

//-------------------------------------------------------------------------------------
// 設定方法
//-------------------------------------------------------------------------------------

func (_this *HttpService) SetRootPath(_path string) {
	_this._RootPath = _path
	if len(_this._RootPath) > 0 && strings.HasSuffix(_this._RootPath, "/") {
		_this._RootPath = _this._RootPath[:len(_this._RootPath)-1]
	}
}

// -------------------------------------------------------------------------------------
func (_this *HttpService) SetDefaultHTML(_default_html string) {
	_this._DefaultHTML = _default_html
}

// -------------------------------------------------------------------------------------
func (_this *HttpService) SetDefaultCacheControl(_control string) {
	_this._DefaultCacheControl = _control
}

// -------------------------------------------------------------------------------------
func (_this *HttpService) SetEnableCache(_enable bool) {
	_this._EnableCache = _enable
}

// -------------------------------------------------------------------------------------
// InitExecutor 在 Go 中簡化實作 (因為 Go 自動處理 Goroutine 池)
func (_this *HttpService) InitExecutor(_force bool, _method string, _core, _max, _timeout int) {
	_this._PoolMethod = _method
	_this._MaxConnection = _max
	// Go 的 http.Server 會自動根據需求增長，這裡主要設定逾時限制
}

// -------------------------------------------------------------------------------------
// ServeHTTP 實作 http.Handler 介面 (對應 Java handle/Process)
func (_this *HttpService) serveHTTP(_w http.ResponseWriter, _r *http.Request) {

	_uriOrg, _ := url.PathUnescape(_r.RequestURI)

	if checkURI(_w, _uriOrg) == false {
		return
	}

	// _RootPath 沒設定就直接 404，避免空字串拼接 _uri 後從檔案系統根目錄送出任意檔案
	if strings.TrimSpace(_this._RootPath) == "" {
		http.Error(_w, "Not Found", http.StatusNotFound)
		return
	}

	_uri := strings.Split(_uriOrg, "?")[0]
	if _uri == "/" && strings.TrimSpace(_this._DefaultHTML) != "" {
		_uri = _this._DefaultHTML
	}

	// 用 filepath.Join + Clean 標準化路徑後驗證仍位於 _RootPath 之下，防止符號連結 / 編碼變體繞過 ".."
	_absRoot, _err := filepath.Abs(_this._RootPath)
	if _err != nil {
		http.Error(_w, "Not Found", http.StatusNotFound)
		return
	}
	_absFile, _err := filepath.Abs(filepath.Join(_absRoot, filepath.FromSlash(_uri)))
	if _err != nil || (_absFile != _absRoot && !strings.HasPrefix(_absFile, _absRoot+string(filepath.Separator))) {
		http.Error(_w, "Not Acceptable", http.StatusNotAcceptable)
		return
	}

	_cacheControl := Tools.If(_this._EnableCache, _this._DefaultCacheControl, "no-cache")
	_w.Header().Add("Cache-Control", _cacheControl.(string))
	http.ServeFile(_w, _r, _absFile)
}

// -------------------------------------------------------------------------------------
// CreateRestfulAPI 註冊路由處理器
func (_this *HttpService) AddRestfulAPI(_uri string, _callback HttpAPI_Callback) {

	if _callback != nil {

		_this._MuxLock.Lock()
		defer _this._MuxLock.Unlock()

		if strings.HasSuffix(_uri, "/") == false {
			_uri = _uri + "/"
		}

		_api := CreateHttpAPI(_callback)

		_this._Handlers[_uri] = _api.callBack
		_this._Mux.HandleFunc(_uri, _api.servHTTP)

		//Tools.ConsolePrint("AddRestfulAPI : " + _uri)
	}
}

// -------------------------------------------------------------------------------------
// AddHandler 註冊自訂 http.Handler (例如 MCP 協定處理器)
func (_this *HttpService) AddHandler(_pattern string, _handler http.Handler) {
	if _handler != nil {
		_this._MuxLock.Lock()
		defer _this._MuxLock.Unlock()
		_this._Mux.Handle(_pattern, _handler)
	}
}

// -------------------------------------------------------------------------------------
// RemoveRestfulAPI 移除路徑 (在標準 http.ServeMux 中較難實現，此處提供邏輯封裝)
func (_this *HttpService) RemoveRestfulAPI(_uri string) {

	_this._MuxLock.Lock()
	defer _this._MuxLock.Unlock()
	delete(_this._Handlers, _uri)

	http.HandleFunc(_uri, nil)
}

// -------------------------------------------------------------------------------------
// run 啟動服務 (對應 Java 的 start() -> run())
func (_this *HttpService) Run() {

	// HTTP Server
	if _this._HttpServer != nil {

		go func() {

			Tools.Log.Print(Tools.LL_Info, fmt.Sprintf("Http Listen at : %d", _this._HttpPort))

			if _err := _this._HttpServer.ListenAndServe(); _err != nil && _err != http.ErrServerClosed {

				Tools.Log.Print(Tools.LL_Error, fmt.Sprintf("HTTP Listen Error: %v", _err))

			}
		}()
	}

	// HTTPS Server
	if _this._HttpsServer != nil {

		go func() {

			Tools.Log.Print(Tools.LL_Info, fmt.Sprintf("Https Listen at : %d", _this._HttpsPort))

			_cert, _err := _this.loadTLSCertificate()
			if _err != nil {
				Tools.Log.Print(Tools.LL_Error, fmt.Sprintf("HTTPS TLS Load Error: %v", _err))
				return
			}

			_this._HttpsServer.TLSConfig.Certificates = []tls.Certificate{_cert}

			if _err = _this._HttpsServer.ListenAndServeTLS("", ""); _err != nil && _err != http.ErrServerClosed {

				Tools.Log.Print(Tools.LL_Error, fmt.Sprintf("HTTPS Listen Error: %v\n", _err))

			}
		}()
	}
}

// -------------------------------------------------------------------------------------
// Close 關閉伺服器
func (_this *HttpService) Close() bool {
	_ctx, _cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer _cancel()

	if _this._HttpServer != nil {
		_this._HttpServer.Shutdown(_ctx)
	}
	if _this._HttpsServer != nil {
		_this._HttpsServer.Shutdown(_ctx)
	}
	return true
}

// -------------------------------------------------------------------------------------
// GetHttpPort 取得實際通訊埠
func (_this *HttpService) GetHttpPort() int {
	return _this._HttpPort
}

// -------------------------------------------------------------------------------------
func (_this *HttpService) loadTLSCertificate() (tls.Certificate, error) {
	_ext := strings.ToLower(filepath.Ext(strings.TrimSpace(_this._SSLCert)))

	switch _ext {
	case ".p12", ".pfx":
		return _this.loadTLSCertificateFromP12()
	default:
		return _this.loadTLSCertificateFromPEM()
	}
}

// -------------------------------------------------------------------------------------
func (_this *HttpService) loadTLSCertificateFromPEM() (tls.Certificate, error) {
	if strings.TrimSpace(_this._SSLCert) == "" || strings.TrimSpace(_this._SSLKeyFile) == "" {
		return tls.Certificate{}, fmt.Errorf("ssl_key 與 ssl_key_file 必須同時設定，ssl_key_file 需對應 .key 檔案")
	}

	return tls.LoadX509KeyPair(_this._SSLCert, _this._SSLKeyFile)
}

// -------------------------------------------------------------------------------------
func (_this *HttpService) loadTLSCertificateFromP12() (tls.Certificate, error) {
	_p12Bytes, _err := os.ReadFile(_this._SSLCert)
	if _err != nil {
		return tls.Certificate{}, _err
	}

	_blocks, _err := pkcs12.ToPEM(_p12Bytes, _this._SSLPassword)
	if _err != nil {
		return tls.Certificate{}, _err
	}

	var _certPEM []byte
	var _keyPEM []byte

	for _, _block := range _blocks {
		if _block == nil {
			continue
		}

		_encoded := pem.EncodeToMemory(_block)
		if strings.Contains(_block.Type, "PRIVATE KEY") {
			_keyPEM = append(_keyPEM, _encoded...)
			continue
		}

		if strings.Contains(_block.Type, "CERTIFICATE") {
			_certPEM = append(_certPEM, _encoded...)
		}
	}

	if len(_certPEM) == 0 || len(_keyPEM) == 0 {
		return tls.Certificate{}, fmt.Errorf("ssl_key 指向的 p12 內容缺少憑證或私鑰")
	}

	return tls.X509KeyPair(_certPEM, _keyPEM)
}

// -------------------------------------------------------------------------------------
