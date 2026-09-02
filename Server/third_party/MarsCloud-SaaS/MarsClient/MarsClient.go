package MarsClient

// -------------------------------------------------------------------------------------
import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Security"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
)

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
// Constants 模擬 Java 靜態常數
const (
	_DefaultSpaceName = "default"
	_DefaultTimeOut   = 15 * 1000
	_Time_OneMin      = 60 * 1000
	_Time_OneHour     = 60 * _Time_OneMin
)

// -------------------------------------------------------------------------------------
type AsyncTaskCallback func(_resp string) bool

// -------------------------------------------------------------------------------------
type MarsClient struct {
	ServerURLs []string

	ProjID    string
	AuthToken string
	Account   string
	Password  string
	SecretKey string

	EnableLoadBalance bool

	// mu 保護以上欄位的並行讀寫；外部仍可直接讀 AuthToken/Account 等欄位（單一指標讀取在 Go 多平台基本原子），
	// 但內部寫入路徑（ReLogin / UpdateServerURL / resetServerURLs）以及讀寫密集的 GetServerURL 必須走鎖
	mu sync.RWMutex
}

// -------------------------------------------------------------------------------------
func Create() *MarsClient {
	return &MarsClient{
		ServerURLs: make([]string, 0),
	}
}

// -------------------------------------------------------------------------------------
// 基礎通訊與登入
// -------------------------------------------------------------------------------------
func (_this *MarsClient) LocalLogin(_proj string) bool {
	_this.mu.Lock()
	_this.ProjID = _proj
	_this.mu.Unlock()

	_api := "/auth/login?"
	if _proj != "" {
		_api = "/auth/login?proj=" + _proj
	}

	_token := _this.CallAPI(_api, "{}", _DefaultTimeOut)

	_this.mu.Lock()
	_this.AuthToken = _token
	_this.mu.Unlock()

	return _token != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) resetServerURLs(_url string) {
	_this.mu.Lock()
	defer _this.mu.Unlock()
	if len(_url) > 4 {
		_cleanURL := strings.ReplaceAll(_url, " ", "")
		_this.ServerURLs = strings.Split(_cleanURL, ",")
	} else {
		_this.ServerURLs = []string{"https://www.mars-cloud.com"}
	}
}

// -------------------------------------------------------------------------------------
// Login 基礎登入
func (_this *MarsClient) Login(_url string, _account string, _pass string) bool {
	_this.resetServerURLs(_url)
	_this.Account = _account
	_this.Password = _pass
	return _this.ReLogin()
}

// -------------------------------------------------------------------------------------
// LoginWithProj 帶有專案 ID 的登入
func (_this *MarsClient) LoginWithProj(_url string, _account string, _pass string, _proj string) bool {
	_this.resetServerURLs(_url)
	_this.Account = _account
	_this.Password = _pass
	_this.ProjID = _proj
	return _this.ReLogin()
}

// -------------------------------------------------------------------------------------
// LoginByToken 使用現有 Token 登入
func (_this *MarsClient) LoginByToken(_url string, _token string) bool {
	_this.resetServerURLs(_url)
	_this.AuthToken = _token
	return _this.ReLogin()
}

// -------------------------------------------------------------------------------------
// LoginByKeyWithProj 使用 Key 與專案 ID 登入
func (_this *MarsClient) LoginByKeyWithProj(_url string, _proj string, _key string) bool {
	_this.resetServerURLs(_url)
	_this.Account = _proj
	_this.ProjID = _proj
	_this.SecretKey = _key
	return _this.ReLogin()
}

// -------------------------------------------------------------------------------------
// LoginByKey 使用 Key 登入
func (_this *MarsClient) LoginByKey(_url string, _key string) bool {
	_this.resetServerURLs(_url)
	_this.SecretKey = _key
	return _this.ReLogin()
}

// -------------------------------------------------------------------------------------
// ReLogin 重新執行登入流程
func (_this *MarsClient) ReLogin() bool {

	_token := ""

	if len(_this.Account) > 0 && len(_this.Password) > 0 {
		_payload := MarsJSON.NewJSONObject(nil)
		_payload.Put("usr", _this.Account)
		_payload.Put("pwd", _this.Password)
		_payload.Put("proj", _this.ProjID)

		_api := "/auth/login?"
		if len(_this.ProjID) > 0 {
			_api = "/auth/login?proj=" + _this.ProjID
		}
		_token = Tools.HttpPost(_this.GetServerURL()+_api, "", "", _payload.ToString(), 0)

	} else if len(_this.SecretKey) > 0 {
		_urlStr := _this.GetServerURL() + "/auth/get_auth_by_key?"
		if len(_this.ProjID) > 0 {
			_urlStr += "proj=" + _this.ProjID
		}
		_token = Tools.HttpPost(_urlStr, "", "", _this.SecretKey, 0)
	} else if len(_this.AuthToken) > 0 {
		// 遵循 Java 原始邏輯：AuthToken 存在時嘗試使用 SecretKey 換取新 Auth
		_token = Tools.HttpPost(_this.GetServerURL()+"/auth/get_auth_by_key?", "", "", _this.SecretKey, 0)
	}

	_this.mu.Lock()
	_this.AuthToken = _token
	_this.mu.Unlock()

	if len(_token) > 10 {
		_this.UpdateServerURL()
		return true
	}
	return false
}

// -------------------------------------------------------------------------------------
// UpdateServerURL 更新負載平衡的伺服器清單
func (_this *MarsClient) UpdateServerURL() {
	if !_this.EnableLoadBalance || len(_this.AuthToken) <= 10 {
		return
	}

	// HTTP 呼叫不在鎖內，避免阻塞所有 GetServerURL 讀取
	_api := _this.GetServerURL() + "/api/get_broker_list?"
	_resp := Tools.HttpPost(_api, _this.AuthToken, "", "", 0)
	if _resp == "" {
		return
	}

	_payload := MarsJSON.NewJSONObject(_resp)
	_results := _payload.OptJSONArray("results")
	if _results == nil || _results.Length() == 0 {
		return
	}

	_urls := make([]string, _results.Length())
	for _i := 0; _i < _results.Length(); _i++ {
		_urls[_i] = _results.OptString(_i, "")
	}

	_this.mu.Lock()
	_this.ServerURLs = _urls
	_this.mu.Unlock()
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) GetServerURLByIndex(_index int) string {
	_this.mu.RLock()
	defer _this.mu.RUnlock()
	if _index >= 0 && _index < len(_this.ServerURLs) {
		return _this.ServerURLs[_index]
	}
	return ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) GetServerURL() string {
	_this.mu.RLock()
	defer _this.mu.RUnlock()
	if len(_this.ServerURLs) == 0 {
		return ""
	}
	_index := 0
	if _this.EnableLoadBalance && len(_this.ServerURLs) > 1 {
		_index = rand.Intn(len(_this.ServerURLs))
	}
	return _this.ServerURLs[_index]
}

// -------------------------------------------------------------------------------------
// CALL API
// -------------------------------------------------------------------------------------
func (_this *MarsClient) CallAPI(_api string, _payload string, _timeout int) string {
	_url := _this.GetServerURL() + _api
	return Tools.HttpPost(_url, _this.AuthToken, "application/json", _payload, _timeout)
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) CallAPISpecify(_url string, _api string, _payload string, _timeout int) string {

	if _url == "" {
		return ""
	}

	return Tools.HttpPost(_url, _this.AuthToken, "application/json", _payload, _timeout)
}

//-------------------------------------------------------------------------------------
// OTA 管理
//-------------------------------------------------------------------------------------

// DownloadOTA 下載遠端更新檔並儲存為檔案
func (_this *MarsClient) DownloadOTA(_srcFile string, _destFile string, _packet string, _version string) bool {

	_payload := MarsJSON.NewJSONObject(nil)
	_payload.Put("packet", _packet)
	_payload.Put("version", _version)
	_payload.Put("file", _srcFile)

	// 模擬 Java 版的高逾時設定 (10 分鐘)
	_api := _this.GetServerURL() + "/op/get_ota_file?"
	_data := Tools.HttpPost(_api, _this.AuthToken, "", _payload.ToString(), 600000)

	if _data != "" {
		_buf, _err := base64.StdEncoding.DecodeString(_data)
		if _err == nil {
			_fErr := os.WriteFile(_destFile, _buf, 0644)
			return _fErr == nil
		}
	}
	return false
}

// -------------------------------------------------------------------------------------
// 安全性管理
// -------------------------------------------------------------------------------------
func (_this *MarsClient) ResetSecurityKey() bool {
	defer Tools.GlobalRecovery()

	if _this.AuthToken == "" {
		return false
	}

	_api := "/sys/get_security_key"
	_req := MarsJSON.NewJSONObject(nil)

	// 1. 獲取 AES Key
	_req.Put("target", "aes")
	_aesBase64 := _this.CallAPI(_api, _req.ToString(), _DefaultTimeOut)
	if _aesBase64 == "" {
		return false
	}
	_aesKey, _ := base64.StdEncoding.DecodeString(_aesBase64)

	Security.JWT.LoadAESKey(_aesKey)

	// 2. 獲取 RSA Keys
	_req.Put("target", "rsa_public")
	_pubBase64 := _this.CallAPI(_api, _req.ToString(), _DefaultTimeOut)
	_pubBase64 = "-----BEGIN PUBLIC KEY-----\n" + _pubBase64 + "\n-----END PUBLIC KEY-----"

	_req.Put("target", "rsa_private")
	_priBase64 := _this.CallAPI(_api, _req.ToString(), _DefaultTimeOut)
	_priBase64 = "-----BEGIN PRIVATE KEY-----\n" + _priBase64 + "\n-----END PRIVATE KEY-----"

	if _pubBase64 != "" && _priBase64 != "" {
		return Security.JWT.LoadRSAKey([]byte(_pubBase64), []byte(_priBase64))
	}

	return false
}

//-------------------------------------------------------------------------------------
// 服務與設備註冊
//-------------------------------------------------------------------------------------

func (_this *MarsClient) RegistryService(_info string, _resetKey bool) bool {
	if _resetKey {
		if !_this.ResetSecurityKey() {
			return false
		}
	}
	_resp := _this.CallAPI("/auth/registry?target=server", _info, _DefaultTimeOut)
	return _resp != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) RegistryServiceProperties(_id string, _prop string) bool {
	_api := fmt.Sprintf("/auth/registry?target=properties&id=%s", _id)
	_resp := _this.CallAPISpecify(_this.GetServerURLByIndex(0), _api, _prop, _DefaultTimeOut)
	return _resp != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) RegistryDevice(_root *MarsJSON.JSONObject) bool {
	if len(_this.AuthToken) < 20 {
		return false
	}
	_resp := _this.CallAPI("/api/usrinfo?method=adddatasrc", _root.ToString(), _DefaultTimeOut)
	return strings.ToLower(_resp) == "ok"
}

// -------------------------------------------------------------------------------------
// 資料存取 (Put/Get)
// -------------------------------------------------------------------------------------
func (_this *MarsClient) PutData(_uuid string, _suid string, _data *MarsJSON.JSONObject) bool {
	if _uuid == "" {
		return false
	}
	_payload := MarsJSON.NewJSONObject(nil)
	_values := MarsJSON.NewJSONArray(nil)
	_values.Put(_data)

	_payload.Put("uuid", _uuid)
	_payload.Put("suid", _suid)
	_payload.Put("values", _values)

	_resp := _this.CallAPI("/api/put?data", _payload.ToString(), _DefaultTimeOut)
	return _resp != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) PutDataAdv(_space, _owner, _uuid, _suid, _ukey string, _data *MarsJSON.JSONObject, _mqttPush bool) bool {
	if _uuid == "" {
		return false
	}
	_payload := MarsJSON.NewJSONObject(nil)
	_values := MarsJSON.NewJSONArray(nil)
	_values.Put(_data)

	_payload.Put("uuid", _uuid)
	_payload.Put("values", _values)

	if _space != "" {
		_payload.Put("space", _space)
	}
	if _owner != "" {
		_payload.Put("user", _owner)
	}
	if _suid != "" {
		_payload.Put("suid", _suid)
	}
	if _ukey != "" {
		_payload.Put("ukey", _ukey)
	}

	_payload.Put("mqtt_push", _mqttPush)

	return _this.PutData(_uuid, _suid, _payload)
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) PutEvent(_uuid string, _suid string, _data *MarsJSON.JSONObject) bool {

	if _uuid == "" {
		return false
	}
	_payload := MarsJSON.NewJSONObject(nil)
	_values := MarsJSON.NewJSONArray(nil)
	_values.Put(_data)

	_payload.Put("uuid", _uuid)
	_payload.Put("suid", _suid)
	_payload.Put("values", _values)

	_resp := _this.CallAPI("/api/put?event", _payload.ToString(), _DefaultTimeOut)

	return _resp != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) GetDataByTime(_uuid string, _startTime int64, _endTime int64) *MarsJSON.JSONObject {

	_data := MarsJSON.NewJSONObject(nil)
	_data.Put("uuid", _uuid)
	_data.Put("timestamp", fmt.Sprintf("%d~%d", _startTime, _endTime))

	_resp := _this.CallAPI("/api/get?data", _data.ToString(), _DefaultTimeOut)
	if _resp == "" {
		return nil
	}
	return MarsJSON.NewJSONObject(_resp)
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) GetDataByTimeAdv(_space, _owner, _uuid, _suid string, _start, _end int64, _compress bool) *MarsJSON.JSONObject {
	if _uuid == "" {
		return nil
	}
	_data := MarsJSON.NewJSONObject(nil)
	_data.Put("uuid", _uuid)
	_data.Put("timestamp", fmt.Sprintf("%d~%d", _start, _end))
	_data.Put("compressed", _compress)
	if _space != "" {
		_data.Put("space", _space)
	}
	if _owner != "" {
		_data.Put("user", _owner)
	}
	if _suid != "" {
		_data.Put("suid", _suid)
	}

	_timeout := _DefaultTimeOut
	if _compress {
		_timeout = _Time_OneHour
	}
	_resp := _this.CallAPI("/api/get?data", _data.ToString(), _timeout)

	if _resp != "" {
		if _compress {
			return _this.UnzipData(_resp)
		}
		return MarsJSON.NewJSONObject(_resp)
	}
	return nil
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) GetLastData(_uuid string, _suid string, _count int, _compress bool) *MarsJSON.JSONObject {

	return _this.GetLastDataAdv("", "", _uuid, _suid, "", "", _count, _compress)
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) GetLastDataAdv(_space, _owner, _uuid, _suid, _orderBy, _orderType string, _count int, _compress bool) *MarsJSON.JSONObject {
	if _uuid == "" {
		return nil
	}
	_data := MarsJSON.NewJSONObject(nil)
	_data.Put("uuid", _uuid)
	_data.Put("count", _count)
	_data.Put("compressed", _compress)
	if _space != "" {
		_data.Put("space", _space)
	}
	if _owner != "" {
		_data.Put("user", _owner)
	}
	if _suid != "" {
		_data.Put("suid", _suid)
	}
	if _orderBy != "" {
		_data.Put("order_by", _orderBy)
	}
	if _orderType != "" {
		_data.Put("order_type", _orderType)
	}

	_timeout := _DefaultTimeOut
	if _compress {
		_timeout = _Time_OneHour
	}
	_resp := _this.CallAPI("/api/lastdata?method=read", _data.ToString(), _timeout)

	if _resp != "" {
		if _compress {
			return _this.UnzipData(_resp)
		}
		return MarsJSON.NewJSONObject(_resp)
	}
	return nil
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) DeleteData(_uuid, _suid, _ukey string, _mqttPush bool) bool {

	return _this.DeleteDataAdv("", "", _uuid, _suid, _ukey, nil, _mqttPush)
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) DeleteDataAdv(_space, _owner, _uuid, _suid, _ukey string, _eventInfo *MarsJSON.JSONObject, _mqttPush bool) bool {
	if _uuid == "" {
		return false
	}
	_data := MarsJSON.NewJSONObject(nil)
	_data.Put("uuid", _uuid)
	_data.Put("ukey", _ukey)
	if _space != "" {
		_data.Put("space", _space)
	}
	if _owner != "" {
		_data.Put("user", _owner)
	}
	if _suid != "" {
		_data.Put("suid", _suid)
	}
	_data.Put("mqtt_push", _mqttPush)
	if _eventInfo != nil {
		_data.Put("event_info", _eventInfo)
	}

	_resp := _this.CallAPI("/api/del?data", _data.ToString(), _DefaultTimeOut)

	return _resp != ""
}

//-------------------------------------------------------------------------------------
// ZIP 資料解壓邏輯
//-------------------------------------------------------------------------------------

func (_this *MarsClient) UnzipData(_dataBase64 string) *MarsJSON.JSONObject {

	if len(_dataBase64) < 16 {
		return nil
	}

	_zipBytes, _err := base64.StdEncoding.DecodeString(_dataBase64)
	if _err != nil {
		return nil
	}

	_reader := bytes.NewReader(_zipBytes)
	_zipReader, _err := zip.NewReader(_reader, int64(len(_zipBytes)))
	if _err != nil {
		return nil
	}

	_results := MarsJSON.NewJSONArray(nil)
	for _, _file := range _zipReader.File {
		_rc, _err := _file.Open()
		if _err != nil || _rc == nil {
			continue
		}
		_buf := new(bytes.Buffer)
		io.Copy(_buf, _rc)
		_rc.Close()

		_obj := MarsJSON.NewJSONObject(_buf.Bytes())
		_items := _obj.OptJSONArray("results")
		if _items != nil {
			for _i := 0; _i < _items.Length(); _i++ {
				_results.Put(_items.Opt(_i))
			}
		}
	}

	_payload := MarsJSON.NewJSONObject(nil)
	_payload.Put("results", _results)

	return _payload
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) UnzipDataHuge(_dataBase64 string) []string {
	_list := make([]string, 0)
	_zipBytes, _ := base64.StdEncoding.DecodeString(_dataBase64)
	_reader, _err := zip.NewReader(bytes.NewReader(_zipBytes), int64(len(_zipBytes)))
	if _err == nil {
		for _, _file := range _reader.File {
			_rc, _openErr := _file.Open()
			if _openErr != nil || _rc == nil {
				continue
			}
			_buf := new(bytes.Buffer)
			io.Copy(_buf, _rc)
			_rc.Close()
			_list = append(_list, _buf.String())
		}
	}
	return _list
}

//-------------------------------------------------------------------------------------
// 非同步任務 (Async Task)
//-------------------------------------------------------------------------------------

func (_this *MarsClient) RunAsyncTask(_taskID string, _service string, _api string, _payload *MarsJSON.JSONObject, _callback AsyncTaskCallback) {
	if _taskID == "" || _api == "" || _callback == nil {
		return
	}

	// 啟動 Goroutine 處理非同步任務 (模擬 Java Thread)
	go func() {
		defer Tools.GlobalRecovery()

		_uri := strings.ReplaceAll(_api, "/", "+")
		_url := fmt.Sprintf("%s/services-async/run/%s/%s/%s", _this.GetServerURL(), _taskID, _service, _uri)
		_resp := Tools.HttpPost(_url, _this.AuthToken, "application/json", _payload.ToString(), 0)

		for _resp != "" {
			time.Sleep(2 * time.Second)
			// 檢查狀態
			_checkURL := fmt.Sprintf("%s/services-async/check/%s", _this.GetServerURL(), _taskID)
			_resp = Tools.HttpGet(_checkURL, _this.AuthToken, 0)

			if _callback(_resp) {
				_resObj := MarsJSON.NewJSONObject(_resp)
				if _resObj.OptBoolean("done", false) {
					break
				}
			}
		}
	}()
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) ReportAsyncTask(_taskID, _status string, _progress int) string {
	if _taskID == "" {
		return ""
	}
	_payload := MarsJSON.NewJSONObject(nil)
	_payload.Put("status", _status)
	_payload.Put("progress", _progress)
	_api := "/services-async/report/" + _taskID
	return _this.CallAPI(_api, _payload.ToString(), _DefaultTimeOut)
}

// -------------------------------------------------------------------------------------
// 其他功能
// -------------------------------------------------------------------------------------
func (_this *MarsClient) PushMail(_host *MarsJSON.JSONObject, _mail, _title, _msg string) bool {
	_obj := MarsJSON.NewJSONObject(nil)
	if _host != nil {
		_obj.Put("from", _host)
	}
	_obj.Put("to", _mail)
	_obj.Put("subject", _title)
	_obj.Put("content_type", "text/plain; charset=UTF-8")
	_obj.Put("content", _msg)
	return _this.CallAPI("/system/send_mail?", _obj.ToString(), _DefaultTimeOut) != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) AddSystemLog(_type, _msg string) bool {
	_obj := MarsJSON.NewJSONObject(nil)
	_obj.Put("type", _type)
	_obj.Put("msg", _msg)
	return _this.CallAPI("/sys/addsyslog?", _obj.ToString(), _DefaultTimeOut) != ""
}

// -------------------------------------------------------------------------------------
func (_this *MarsClient) SendBroadcast(_from string, _msg *MarsJSON.JSONObject) bool {
	_payload := MarsJSON.NewJSONObject(nil)
	_values := MarsJSON.NewJSONArray(nil)
	_values.Put(_msg)

	_payload.Put("uuid", "broadcast")
	_payload.Put("suid", _from)
	_payload.Put("target", "event")
	_payload.Put("topic", "+/event/broadcast")
	_payload.Put("values", _values)

	return _this.CallAPI("/sys/broadcast", _payload.ToString(), _DefaultTimeOut) != ""
}

// -------------------------------------------------------------------------------------
