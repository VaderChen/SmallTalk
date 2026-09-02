package MarsJSON

// -------------------------------------------------------------------------------------
import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

// -------------------------------------------------------------------------------------
// JSONObject 實作
// -------------------------------------------------------------------------------------

type JSONObject struct {
	_Src map[string]interface{}
}

// -------------------------------------------------------------------------------------
// NewJSONObject 建立空的或是從字串/位元組解析 JSON 物件
func NewJSONObject(_input interface{}) *JSONObject {

	if _input != nil {

		switch _v := _input.(type) {
		case string:
			{
				_v = fixJsonString(_v)
				_json := make(map[string]interface{})
				_err := json.Unmarshal([]byte(_v), &_json)

				if _err != nil {
					//fmt.Println(_err)
					return &JSONObject{_Src: make(map[string]interface{})}
				}

				return &JSONObject{_Src: _json}
			}
		case []byte:
			{
				_json := make(map[string]interface{})
				_err := json.Unmarshal(_v, &_json)

				if _err != nil {
					//fmt.Println(_err)
					return &JSONObject{_Src: make(map[string]interface{})}
				}

				return &JSONObject{_Src: _json}
			}
		case map[string]interface{}:
			return &JSONObject{_Src: _v}
		default:
			break
		}
	}

	return &JSONObject{_Src: make(map[string]interface{})}
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) Put(_key string, _value interface{}) *JSONObject {
	if _this._Src == nil {
		_this._Src = make(map[string]interface{})
	}
	// 處理嵌套的封裝物件
	switch _v := _value.(type) {
	case *JSONObject:
		_this._Src[_key] = _v._Src
	case *JSONArray:
		_this._Src[_key] = _v._Src
	default:
		_this._Src[_key] = _value
	}
	return _this
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) OptString(_key string, _defaultValue string) string {

	if _val, _ok := _this._Src[_key]; _ok && _val != nil {
		return fmt.Sprintf("%v", _val)
	}
	return _defaultValue
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) OptInt(_key string, _defaultValue int) int {
	if _val, _ok := _this._Src[_key]; _ok && _val != nil {
		switch _v := _val.(type) {
		case float64: // Go json 預設數字為 float64
			return int(_v)
		case int:
			return _v
		case int32:
			return int(_v)
		case int64:
			return int(_v)
		case string:
			// 模擬 Java 的數字過濾邏輯
			_reg := regexp.MustCompile(`[^0-9]`)
			_clean := _reg.ReplaceAllString(_v, "")
			_res, _err := strconv.Atoi(_clean)
			if _err == nil {
				return _res
			}
		}
	}
	return _defaultValue
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) OptLong(_key string, _defaultValue int64) int64 {
	if _val, _ok := _this._Src[_key]; _ok && _val != nil {
		switch _v := _val.(type) {
		case float64:
			return int64(_v)
		case int:
			return int64(_v)
		case int32:
			return int64(_v)
		case int64:
			return _v
		case string:
			_reg := regexp.MustCompile(`[^0-9]`)
			_clean := _reg.ReplaceAllString(_v, "")
			_res, _err := strconv.ParseInt(_clean, 10, 64)
			if _err == nil {
				return _res
			}
		}
	}
	return _defaultValue
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) OptBoolean(_key string, _defaultValue bool) bool {
	if _val, _ok := _this._Src[_key]; _ok && _val != nil {
		switch _v := _val.(type) {
		case bool:
			return _v
		case string:
			_res, _err := strconv.ParseBool(_v)
			if _err == nil {
				return _res
			}
		}
	}
	return _defaultValue
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) OptJSONObject(_key string) *JSONObject {
	if _val, _ok := _this._Src[_key]; _ok {
		if _m, _isMap := _val.(map[string]interface{}); _isMap {
			return NewJSONObject(_m)
		}
	}
	return NewJSONObject(nil)
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) OptJSONArray(_key string) *JSONArray {
	if _val, _ok := _this._Src[_key]; _ok {
		if _a, _isSlice := _val.([]interface{}); _isSlice {
			return NewJSONArray(_a)
		}
	}
	return NewJSONArray(nil)
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) Opt(_key string) any {
	if _val, _ok := _this._Src[_key]; _ok {
		return _val
	}
	return nil
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) Remove(_key string) interface{} {
	if _this._Src == nil {
		return nil
	}

	// 先取得該值，以便模擬 Java 的回傳行為
	_val, _exists := _this._Src[_key]
	if _exists {
		delete(_this._Src, _key)
		return _val
	}

	return nil
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) ToString() string {
	_b, _ := json.Marshal(_this._Src)
	return string(_b)
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) ToPrettyString() string {
	_b, _ := json.MarshalIndent(_this._Src, "", "  ")
	return string(_b)
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) Length() int {
	return len(_this._Src)
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) Has(_key string) bool {
	_, _ok := _this._Src[_key]
	return _ok
}

// -------------------------------------------------------------------------------------
func (_this *JSONObject) MergeFrom(_extInfo *JSONObject) {
	// 使用 defer 與 GlobalRecovery 處理可能的異常
	defer func() {
		if _r := recover(); _r != nil {
			// 模擬 Java 的 ExceptionMsgPrintOut
			fmt.Printf("MergeFrom Error: %v\n", _r)
		}
	}()

	// 如果傳入的物件不為空
	if _extInfo != nil && _extInfo._Src != nil {
		// 遍歷傳入物件的所有鍵值
		for _key, _val := range _extInfo._Src {
			// 將鍵值對存入當前的 map 中
			_this._Src[_key] = _val
		}
	}
}

// -------------------------------------------------------------------------------------
