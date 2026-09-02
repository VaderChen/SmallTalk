package Security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwe"
)

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
type TokenMethod string

// -------------------------------------------------------------------------------------
const (
	TM_NONE  TokenMethod = "NONE"
	TM_AES   TokenMethod = "AES"
	TM_AES32 TokenMethod = "AES32"
	TM_RSA   TokenMethod = "RSA"
)

// -------------------------------------------------------------------------------------
func (_tm TokenMethod) Value() string {
	return string(_tm)
}

// -------------------------------------------------------------------------------------
// JWTProcessor 結構體
// -------------------------------------------------------------------------------------
type JWTProcessor struct {
	_SecretKey  []byte
	_PublicKey  *rsa.PublicKey
	_PrivateKey *rsa.PrivateKey
	_AESBlock   cipher.Block
	_AES_IV     []byte
	_mu         sync.RWMutex // 保護以上欄位的並行讀寫；外部 method 拿鎖，內部 *Locked helper 不再拿
}

// -------------------------------------------------------------------------------------
// 全域實例 (模擬 Java 的靜態成員)
var JWT = &JWTProcessor{
	_AES_IV: generateNumericString(16), // 與 Java 相同的 IV
}

// -------------------------------------------------------------------------------------
func generateNumericString(length int) []byte {
	const charset = "0123456789"

	_result := make([]byte, length)
	for i := range _result {
		// 安全地生成 0-9 的隨機索引
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return nil
		}

		_result[i] = charset[num.Int64()]
	}

	return _result
}

// -------------------------------------------------------------------------------------
// RSA 相關功能
// -------------------------------------------------------------------------------------
// NewRSAKey 重新產生並儲存 RSA 金鑰
func (_this *JWTProcessor) NewRSAKey(_method string, _pubPath string, _priPath string) bool {
	os.Remove(_pubPath)
	os.Remove(_priPath)
	return _this.LoadRSAKeyFromFile(_pubPath, _priPath)
}

// -------------------------------------------------------------------------------------
// LoadRSAKey 從位元組載入 RSA 金鑰
func (_this *JWTProcessor) LoadRSAKey(_pubKey []byte, _priKey []byte) bool {
	_this._mu.Lock()
	defer _this._mu.Unlock()
	return _this.loadRSAKeyLocked(_pubKey, _priKey)
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) loadRSAKeyLocked(_pubKey []byte, _priKey []byte) bool {

	if len(_pubKey) > 0 && len(_priKey) > 0 {

		// 解析公鑰 (X509)
		_blockPub, _ := pem.Decode(_pubKey)
		if _blockPub != nil {
			_pub, _err := x509.ParsePKIXPublicKey(_blockPub.Bytes)
			if _err == nil {
				_this._PublicKey = _pub.(*rsa.PublicKey)
			}
		}

		// 解析私鑰 (PKCS8)
		_blockPri, _ := pem.Decode(_priKey)
		if _blockPri != nil {
			_pri, _err := x509.ParsePKCS8PrivateKey(_blockPri.Bytes)
			if _err == nil {
				_this._PrivateKey = _pri.(*rsa.PrivateKey)
			}
		}
	}

	if _this._PublicKey == nil || _this._PrivateKey == nil {

		Tools.Log.Print(Tools.LL_Warning, "JWS RSA Key is empty, dynamic generating ...")
		_pri, _err := rsa.GenerateKey(rand.Reader, 2048) // Go 建議至少 2048
		if _err != nil {
			return false
		}

		_this._PrivateKey = _pri
		_this._PublicKey = &_pri.PublicKey
	}

	Tools.Log.Print(Tools.LL_Info, "JWS RSA Key is ready")
	return true
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) LoadRSAKeyFromFile(_pubPath string, _priPath string) bool {
	_pubBinary, _ := os.ReadFile(_pubPath)
	_priBinary, _ := os.ReadFile(_priPath)

	_this._mu.Lock()
	_resp := _this.loadRSAKeyLocked(_pubBinary, _priBinary)
	_this._mu.Unlock()

	if _pubBinary == nil || _priBinary == nil {
		_this.SaveRSAKey(_pubPath, _priPath)
	}
	return _resp
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) SaveRSAKey(_pubPath string, _priPath string) bool {
	_this._mu.RLock()
	defer _this._mu.RUnlock()

	if _this._PublicKey != nil && _this._PrivateKey != nil {
		_pubASN1, _ := x509.MarshalPKIXPublicKey(_this._PublicKey)
		_priASN1, _ := x509.MarshalPKCS8PrivateKey(_this._PrivateKey)

		_pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: _pubASN1})
		_priPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: _priASN1})

		os.WriteFile(_pubPath, _pubPEM, 0644)
		os.WriteFile(_priPath, _priPEM, 0600)
		return true
	}
	return false
}

//-------------------------------------------------------------------------------------
// AES 相關功能
//-------------------------------------------------------------------------------------

func (_this *JWTProcessor) LoadAESKey(_key []byte) bool {
	_this._mu.Lock()
	defer _this._mu.Unlock()
	return _this.loadAESKeyLocked(_key, 16)
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) LoadAESKey32(_key []byte) bool {
	_this._mu.Lock()
	defer _this._mu.Unlock()
	return _this.loadAESKeyLocked(_key, 32)
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) loadAESKeyLocked(_key []byte, _size int) bool {
	if len(_key) > 0 {
		_this._SecretKey = _key
	} else {
		Tools.Log.Print(Tools.LL_Warning, "JWS AES Key is empty, generating ...")
		_this._SecretKey = make([]byte, _size)
		io.ReadFull(rand.Reader, _this._SecretKey)
	}

	_block, _err := aes.NewCipher(_this._SecretKey)
	if _err != nil {
		return false
	}
	_this._AESBlock = _block
	Tools.Log.Print(Tools.LL_Info, "JWS AES Key is ready")
	return true
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) LoadAESKeyFromFile(_path string) bool {
	_key, _ := os.ReadFile(_path)

	_this._mu.Lock()
	_resp := _this.loadAESKeyLocked(_key, 16)
	_secret := _this._SecretKey
	_this._mu.Unlock()

	if _key == nil {
		os.WriteFile(_path, _secret, 0600)
	}
	return _resp
}

// -------------------------------------------------------------------------------------
// Token (JWE) 加解密
// -------------------------------------------------------------------------------------
// CreateToken 建立 JWE Token (支援 RSA 與 AES/Direct)
func (_this *JWTProcessor) CreateToken(_method string, _root map[string]interface{}) string {
	_this._mu.RLock()
	defer _this._mu.RUnlock()

	_payload, _ := json.Marshal(_root)
	_headers := jwe.NewHeaders()
	_headers.Set("com", "mars-semi.com")

	var _token []byte
	var _err error

	if _method == TM_RSA.Value() && _this._PublicKey != nil {
		// 與舊 Java 相容：使用 RSA-OAEP + A128GCM
		_token, _err = jwe.Encrypt(
			_payload,
			jwe.WithKey(jwa.RSA_OAEP, _this._PublicKey),
			jwe.WithContentEncryption(jwa.A128GCM),
			jwe.WithProtectedHeaders(_headers),
		)
	} else if _method == TM_AES.Value() && len(_this._SecretKey) > 0 {
		// 直接使用 AES Key 加密 (Direct)
		_token, _err = jwe.Encrypt(_payload, jwe.WithKey(jwa.DIRECT, _this._SecretKey), jwe.WithContentEncryption(jwa.A128GCM), jwe.WithProtectedHeaders(_headers))
	} else if _method == TM_AES32.Value() && len(_this._SecretKey) > 0 {
		// 直接使用 AES Key 加密 (Direct)
		_token, _err = jwe.Encrypt(_payload, jwe.WithKey(jwa.DIRECT, _this._SecretKey), jwe.WithProtectedHeaders(_headers))
	}

	if _err != nil {
		Tools.Log.Print(Tools.LL_Debug, fmt.Sprintf("JWS Create Error : %v", _err))
		return ""
	}

	return string(_token)
}

// -------------------------------------------------------------------------------------
// DecryptToken 解密並驗證 Token
func (_this *JWTProcessor) DecryptToken(_tokenStr string, _ignoreExp bool) *MarsJSON.JSONObject {
	if _tokenStr == "" {
		return nil
	}

	_this._mu.RLock()
	defer _this._mu.RUnlock()

	var _decrypted []byte
	var _err error

	// 嘗試使用私鑰解密 (RSA)
	if _this._PrivateKey != nil {
		_decrypted, _err = jwe.Decrypt([]byte(_tokenStr), jwe.WithKey(jwa.RSA_OAEP, _this._PrivateKey))
		if _err != nil {
			_decrypted, _err = jwe.Decrypt([]byte(_tokenStr), jwe.WithKey(jwa.RSA_OAEP_256, _this._PrivateKey))
		}
	}

	// 若 RSA 失敗或無私鑰，嘗試 AES
	if _err != nil && len(_this._SecretKey) > 0 {
		_decrypted, _err = jwe.Decrypt([]byte(_tokenStr), jwe.WithKey(jwa.DIRECT, _this._SecretKey))
	}

	if _err != nil {
		return nil
	}

	// 解析 Payload
	var _obj map[string]interface{}
	json.Unmarshal(_decrypted, &_obj)

	// 驗證有效期 (exp)
	if _exp, _ok := _obj["exp"].(float64); _ok {
		if !_ignoreExp && int64(_exp) < time.Now().Unix() {
			Tools.Log.Print(Tools.LL_Debug, "JWS token is out of time")
			return nil
		}
	}

	return MarsJSON.NewJSONObject(_obj)
}

// -------------------------------------------------------------------------------------
// AES Data 加解密 (CBC)
// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) EncryptAESData(_id string) string {
	_this._mu.RLock()
	defer _this._mu.RUnlock()

	if _this._AESBlock == nil {
		return ""
	}
	_plaintext := []byte(_id)
	// PKCS7 Padding
	_padding := aes.BlockSize - (len(_plaintext) % aes.BlockSize)
	_padtext := append(_plaintext, bytes.Repeat([]byte{byte(_padding)}, _padding)...)

	_ciphertext := make([]byte, len(_padtext))
	_mode := cipher.NewCBCEncrypter(_this._AESBlock, _this._AES_IV)
	_mode.CryptBlocks(_ciphertext, _padtext)

	return base64.StdEncoding.EncodeToString(_ciphertext)
}

// -------------------------------------------------------------------------------------
func (_this *JWTProcessor) DecryptAESData(_data string) string {
	_this._mu.RLock()
	defer _this._mu.RUnlock()

	if _this._AESBlock == nil {
		return ""
	}
	_ciphertext, _ := base64.StdEncoding.DecodeString(_data)
	_mode := cipher.NewCBCDecrypter(_this._AESBlock, _this._AES_IV)
	_mode.CryptBlocks(_ciphertext, _ciphertext)

	// Unpadding
	_padding := int(_ciphertext[len(_ciphertext)-1])
	return string(_ciphertext[:len(_ciphertext)-_padding])
}
