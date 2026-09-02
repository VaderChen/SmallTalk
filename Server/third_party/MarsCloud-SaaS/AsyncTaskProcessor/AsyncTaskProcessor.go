package AsyncTaskProcessor

// -------------------------------------------------------------------------------------
import (
	"strings"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsClient"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
)

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
// AsyncTaskProcessor 處理非同步任務的處理器
type AsyncTaskProcessor struct {
	_Client            *MarsClient.MarsClient
	_MainServerHost    string
	_MainServerWebhook string
}

// -------------------------------------------------------------------------------------
// NewAsyncTaskProcessor 建立處理器實例
func Create(_client *MarsClient.MarsClient, _webHook string) *AsyncTaskProcessor {

	_this := &AsyncTaskProcessor{}

	if _client != nil {

		_this._Client = _client
		if _this._Client != nil {
			_this._MainServerHost = _this._Client.GetServerURLByIndex(0)
			_this._MainServerWebhook = _webHook
		}
	}

	return _this
}

// -------------------------------------------------------------------------------------
// OnMQTTMessage 接收並解析 MQTT 訊息
func (_this *AsyncTaskProcessor) OnMQTTMessage(_topic string, _payload string) {

	_topics := strings.Split(_topic, "/")

	// Java: if(_topics.length >= 3) switch(_topics[2])
	if len(_topics) >= 3 {
		switch _topics[2] {
		case "api":
			_this.ProcessAPI(_payload)
		}
	}
}

// -------------------------------------------------------------------------------------
// ProcessAPI 啟動一個 Goroutine 來執行非同步任務 (對應 Java 的 new Thread().start())
func (_this *AsyncTaskProcessor) ProcessAPI(_payload string) {

	go func() {

		// 1. 執行非同步任務邏輯 (對應 AsyncTask.run)
		_content := MarsJSON.NewJSONObject(_payload)
		_api := _content.OptString("api", "")
		_token := _content.OptString("token", "")
		_body := _content.OptString("body", "")
		_resp := Tools.HttpPost(_this._MainServerWebhook+_api, _token, "", _body, 7200000)

		_content.Remove("token") // 移除 token 不回傳
		// 同時送出 respone（與 Java 舊版相容）與 response（拼字修正版），讓新舊接收端都能解析
		_content.Put("respone", _resp)
		_content.Put("response", _resp)
		_payload = _content.ToString()

		// 2. 回傳結果給主伺服器 (最後執行的 finally 區塊)
		if _this._Client != nil {
			_respURL := _this._MainServerHost + "/services/respone"
			Tools.HttpPost(_respURL, _this._Client.AuthToken, "", _payload, 15000)
		}
	}()
}

// -------------------------------------------------------------------------------------
