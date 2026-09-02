package ScriptExecutor

// -------------------------------------------------------------------------------------
import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
	"github.com/dop251/goja"
)

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
// EngineNode 存放腳本節點資訊
type EngineNode struct {
	_ID             int
	_VM             *goja.Runtime
	_Program        *goja.Program // 預編譯的腳本
	_FileName       string
	_PrevScriptSize int64
	_PrevLoadTime   int64
	_ReloadInterval int // 秒
	_IsCompiled     bool
}

// -------------------------------------------------------------------------------------
type JavaScriptExecutor struct {
	_Nodes []*EngineNode
	_Mutex sync.Mutex // 確保執行緒安全
}

// -------------------------------------------------------------------------------------
// NewJavaScriptExecutor 建立執行器並初始化預設指令
func CreateJSExecutor() *JavaScriptExecutor {

	_this := &JavaScriptExecutor{
		_Nodes: make([]*EngineNode, 0),
	}

	return _this
}

// -------------------------------------------------------------------------------------
// 內部方法：為 VM 注入預設指令 (Sleep, XMLHttpRequest, TextFileReader)
func (_this *JavaScriptExecutor) _bindDefaultCommands(_vm *goja.Runtime) {
	// 1. Sleep 功能
	_vm.Set("Sleep", func(_ms int) int {
		time.Sleep(time.Duration(_ms) * time.Millisecond)
		return _ms
	})

	// 2. XMLHttpRequest 功能 (封裝之前的 netutil)
	_vm.Set("XMLHttpRequest", func(_jsonParams string) string {
		var _p map[string]interface{}
		json.Unmarshal([]byte(_jsonParams), &_p)

		_url, _ := _p["url"].(string)
		_auth, _ := _p["auth"].(string)
		_payload, _ := _p["payload"].(string)
		_timeout, _ := _p["timeout"].(float64)

		if _url != "" {
			if _payload != "" {
				// 呼叫之前轉換過的 HttpPost
				return string(Tools.HttpPost_BytesData(_url, _auth, true, "application/json", _payload, int(_timeout)))
			}
			return Tools.HttpGet(_url, _auth, int(_timeout))
		}
		return ""
	})

	// 3. TextFileReader 功能
	_vm.Set("TextFileReader", func(_fn string) string {
		// 呼叫之前實作的檔案讀取工具 (假設在同套件或 util 下)
		_data, _err := os.ReadFile(_fn)
		if _err != nil {
			return ""
		}
		return string(_data)
	})
}

// -------------------------------------------------------------------------------------
// ReloadFromScript 從字串載入並執行/編譯腳本
func (_this *JavaScriptExecutor) ReloadFromScript(_id int, _script string, _isNeedCompile bool) int {
	_this._Mutex.Lock()
	defer _this._Mutex.Unlock()

	var _node *EngineNode
	if _id >= 0 && _id < len(_this._Nodes) {
		_node = _this._Nodes[_id]
	} else {
		_node = &EngineNode{
			_ID: len(_this._Nodes),
			_VM: goja.New(),
		}
		_this._bindDefaultCommands(_node._VM)
	}

	_node._PrevLoadTime = time.Now().UnixMilli()
	_node._PrevScriptSize = int64(len(_script))

	if _isNeedCompile {
		_prog, _err := goja.Compile("", _script, false)
		if _err == nil {
			_node._Program = _prog
			_node._IsCompiled = true
			_node._VM.RunProgram(_prog)
		}
	} else {
		_node._VM.RunString(_script)
	}

	if _id < 0 {
		_this._Nodes = append(_this._Nodes, _node)
	}
	return _node._ID
}

// -------------------------------------------------------------------------------------
// LoadFromFile 從檔案載入腳本，支援自動重新載入
func (_this *JavaScriptExecutor) LoadFromFile(_fn string, _isCompile bool, _reloadInterval int) int {
	_data, _err := os.ReadFile(_fn)
	if _err != nil {
		return -1
	}

	_id := _this.ReloadFromScript(-1, string(_data), _isCompile)
	if _id >= 0 {
		_node := _this._Nodes[_id]
		_node._FileName = _fn
		_node._ReloadInterval = _reloadInterval
		_fInfo, _ := os.Stat(_fn)
		_node._PrevScriptSize = _fInfo.Size()
	}
	return _id
}

// -------------------------------------------------------------------------------------
// Call 呼叫特定腳本節點中的 Function
func (_this *JavaScriptExecutor) Call(_id int, _funcName string, _params ...interface{}) interface{} {
	_this._Mutex.Lock()
	_nodeCount := len(_this._Nodes)
	if _id < 0 || _id >= _nodeCount {
		_this._Mutex.Unlock()
		return nil
	}
	_node := _this._Nodes[_id]
	_this._Mutex.Unlock()

	// 檢查是否需要重新載入檔案
	if _node._ReloadInterval > 0 && _node._FileName != "" {
		_now := time.Now().UnixMilli()
		if _now-_node._PrevLoadTime > int64(_node._ReloadInterval*1000) {
			_fInfo, _err := os.Stat(_node._FileName)
			if _err == nil && _fInfo.Size() != _node._PrevScriptSize {
				_data, _ := os.ReadFile(_node._FileName)
				_this.ReloadFromScript(_node._ID, string(_data), _node._IsCompiled)
			}
		}
	}

	// 取得 JS 中的 Function
	_fn, _ok := goja.AssertFunction(_node._VM.Get(_funcName))
	if !_ok {
		return nil
	}

	// 轉換參數為 Goja 格式
	_jsParams := make([]goja.Value, len(_params))
	for _i, _p := range _params {
		_jsParams[_i] = _node._VM.ToValue(_p)
	}

	_res, _err := _fn(goja.Undefined(), _jsParams...)
	if _err != nil {
		return nil
	}

	return _res.Export()
}

// -------------------------------------------------------------------------------------
// Execute 直接在預設環境中執行一段 JS 代碼
func (_this *JavaScriptExecutor) Execute(_script string) interface{} {
	_vm := goja.New()
	_this._bindDefaultCommands(_vm)
	_res, _err := _vm.RunString(_script)
	if _err != nil {
		return nil
	}
	return _res.Export()
}

// -------------------------------------------------------------------------------------
