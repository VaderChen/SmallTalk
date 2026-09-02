package Security

// -------------------------------------------------------------------------------------
import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
)

// -------------------------------------------------------------------------------------
// failureBackoff 對驗證失敗加入隨機延遲，仍保留 brute-force 阻擋效果但比固定 500ms 更節省 server thread，
// jitter 也能擾亂時間旁通道
func failureBackoff() {
	time.Sleep(time.Duration(80+rand.Intn(120)) * time.Millisecond)
}

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
type UserGroup string

// -------------------------------------------------------------------------------------
const (
	UT_Administrator UserGroup = "administrator"
	UT_User          UserGroup = "user"
	UT_Guest         UserGroup = "guest"
)

var (
	compatJWTVerifiers     []*JWTProcessor
	compatJWTVerifiersOnce sync.Once
)

// ------------------------------------------------------------------------------------
// VerifyToken 驗證 Token 合法性與權限群組
func VerifyToken(_auth_string string, _group UserGroup, _ipadd_from string) *MarsJSON.JSONObject {
	// 使用 defer 捕捉異常並列印訊息
	defer Tools.GlobalRecovery()

	// 1. 執行解密
	_jobj := DecryptToken(_auth_string, false)

	if _jobj != nil {

		// 2. 驗證群組是否匹配 (如果 _group 為空則跳過驗證)
		_tokenGroup := strings.ToLower(_jobj.OptString("group", ""))

		if _group == "" || string(_group) == _tokenGroup {

			// 3. 記錄來源 IP 並回傳
			_connectFrom := ""
			if _ipadd_from != "" {
				_connectFrom = _ipadd_from
			}

			_jobj.Put("connect_from", _connectFrom)

			return _jobj
		}

		Tools.Log.Print(Tools.LL_Debug, "User Group Verify Error : %s != %s", _tokenGroup, _group)

	} else {

		// 4. 失敗時記錄 Debug Log
		if _ipadd_from != "" {
			Tools.Log.Print(Tools.LL_Debug, "Verify token fail from "+_ipadd_from)
		} else {
			Tools.Log.Print(Tools.LL_Debug, "Verify token fail : "+_auth_string)
		}
	}

	return nil
}

// -------------------------------------------------------------------------------------
// DecryptToken 執行 JWT 解密並處理逾時邏輯
func DecryptToken(_auth_string string, _ignore_timetolive bool) *MarsJSON.JSONObject {
	defer Tools.GlobalRecovery()

	_token := extractRawToken(_auth_string)
	if len(_token) > 16 {
		_jobj := JWT.DecryptToken(_token, _ignore_timetolive)
		if _jobj != nil {
			return _jobj
		}

		for _, _verifier := range getCompatJWTVerifiers() {
			if _verifier == nil {
				continue
			}
			_jobj = _verifier.DecryptToken(_token, _ignore_timetolive)
			if _jobj != nil {
				return _jobj
			}
		}

		if !_ignore_timetolive {
			failureBackoff()
		}
		return nil
	}

	if len(_auth_string) > 16 {
		_jobj := JWT.DecryptToken(_auth_string, _ignore_timetolive)
		if _jobj != nil {
			return _jobj
		}
	}

	// 3. 失敗時強制延遲，防止網路暴力攻擊
	failureBackoff()

	return nil
}

// -------------------------------------------------------------------------------------
func extractRawToken(_authString string) string {
	_authString = strings.TrimSpace(_authString)
	if _authString == "" {
		return ""
	}

	if strings.Contains(_authString, " ") {
		_parts := strings.SplitN(_authString, " ", 2)
		// 只認 Bearer scheme，避免把 "Basic xxx" 等其他驗證 header 當成 JWT 解析
		if !strings.EqualFold(strings.TrimSpace(_parts[0]), "Bearer") {
			return ""
		}
		return strings.TrimSpace(_parts[1])
	}

	return _authString
}

// -------------------------------------------------------------------------------------
func getCompatJWTVerifiers() []*JWTProcessor {
	compatJWTVerifiersOnce.Do(initCompatJWTVerifiers)
	return compatJWTVerifiers
}

// -------------------------------------------------------------------------------------
func initCompatJWTVerifiers() {
	compatJWTVerifiers = make([]*JWTProcessor, 0)

	_prop := loadCompatProperty()
	_candidates := []struct {
		aes string
		pub string
		pri string
	}{
		{
			aes: strings.TrimSpace(_prop.OptString("legacy_aes", "")),
			pub: strings.TrimSpace(_prop.OptString("legacy_rsa_pub", "")),
			pri: strings.TrimSpace(_prop.OptString("legacy_rsa_pri", "")),
		},
		{
			aes: strings.TrimSpace(_prop.OptString("compat_aes", "")),
			pub: strings.TrimSpace(_prop.OptString("compat_rsa_pub", "")),
			pri: strings.TrimSpace(_prop.OptString("compat_rsa_pri", "")),
		},
		{
			aes: strings.TrimSpace(_prop.OptString("default_aes", "")),
			pub: strings.TrimSpace(_prop.OptString("default_rsa_pub", "")),
			pri: strings.TrimSpace(_prop.OptString("default_rsa_pri", "")),
		},
		{
			aes: "./authhub.aes.key",
			pub: "./authhub.rsa.pub",
			pri: "./authhub.rsa.pri",
		},
		{
			aes: "./cert/aes.key",
			pub: "./cert/rsa.pub",
			pri: "./cert/rsa.pri",
		},
		{
			aes: "../Cert/aes.key",
			pub: "../Cert/rsa.pub",
			pri: "../Cert/rsa.pri",
		},
	}

	_seen := map[string]bool{}
	for _, _candidate := range _candidates {
		_aes := normalizeCompatPath(_candidate.aes)
		_pub := normalizeCompatPath(_candidate.pub)
		_pri := normalizeCompatPath(_candidate.pri)
		if _aes == "" || _pub == "" || _pri == "" {
			continue
		}
		if !compatFileExists(_aes) || !compatFileExists(_pub) || !compatFileExists(_pri) {
			continue
		}

		_key := _aes + "|" + _pub + "|" + _pri
		if _seen[_key] {
			continue
		}
		_seen[_key] = true

		_jwt := &JWTProcessor{}
		if !_jwt.LoadRSAKeyFromFile(_pub, _pri) {
			continue
		}
		if !_jwt.LoadAESKeyFromFile(_aes) {
			continue
		}

		compatJWTVerifiers = append(compatJWTVerifiers, _jwt)
	}
}

// -------------------------------------------------------------------------------------
func loadCompatProperty() *MarsJSON.JSONObject {
	_candidates := []string{
		"./agent.properties",
		"../agent.properties",
	}

	for _, _path := range _candidates {
		if !compatFileExists(_path) {
			continue
		}

		return MarsJSON.NewJSONObject(Tools.File2String(_path))
	}

	return MarsJSON.NewJSONObject("{}")
}

// -------------------------------------------------------------------------------------
func normalizeCompatPath(_path string) string {
	_path = strings.TrimSpace(_path)
	if _path == "" {
		return ""
	}

	return filepath.Clean(_path)
}

// -------------------------------------------------------------------------------------
func compatFileExists(_path string) bool {
	_info, _err := os.Stat(_path)
	return _err == nil && !_info.IsDir()
}

// -------------------------------------------------------------------------------------
