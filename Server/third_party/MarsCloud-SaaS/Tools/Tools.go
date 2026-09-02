package Tools

//-------------------------------------------------------------------------------------
import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/mem"
)

// -------------------------------------------------------------------------------------
// 模擬 Java 的常量
// -------------------------------------------------------------------------------------
const (
	DefaultCharset = "UTF-8"
	DefaultTimeout = 15 * time.Second
)

// -------------------------------------------------------------------------------------
// DefaultInsecureTLS 控制 HttpPost / MQTT TLS 是否略過憑證驗證，預設保留舊版行為（true）
// 由 MarsService 在初始化時依 properties 中的 tls_skip_verify 決定，可設成 false 強制驗證
var DefaultInsecureTLS = true

//-------------------------------------------------------------------------------------
// Stopwatch 區段
//-------------------------------------------------------------------------------------

type Stopwatch struct {
	start time.Time
}

// -------------------------------------------------------------------------------------
func CreateStopwatch() *Stopwatch {
	_sw := &Stopwatch{}
	_sw.Reset()
	return _sw
}

// -------------------------------------------------------------------------------------
func (_sw *Stopwatch) Reset() {
	_sw.start = time.Now()
}

// -------------------------------------------------------------------------------------
func (_sw *Stopwatch) Get() int64 {
	return time.Since(_sw.start).Milliseconds()
}

//-------------------------------------------------------------------------------------
// 時間工具
//-------------------------------------------------------------------------------------

func GetLocalTime() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// -------------------------------------------------------------------------------------
func GetUTC() int64 {
	return time.Now().UTC().UnixNano() / int64(time.Millisecond)
}

// -------------------------------------------------------------------------------------
func GetCurrentDateTimeString() string {
	return time.Now().Format("01/02 15:04:05")
}

// -------------------------------------------------------------------------------------
func Sleep(_ms int) {
	time.Sleep(time.Duration(_ms) * time.Millisecond)
}

//-------------------------------------------------------------------------------------
// 系統資訊與 OS 判斷
//-------------------------------------------------------------------------------------

func GetOSName() string {
	return runtime.GOOS
}

// -------------------------------------------------------------------------------------
func IsMSWindow() bool {
	return runtime.GOOS == "windows"
}

// -------------------------------------------------------------------------------------
func IsMacOS() bool {
	return runtime.GOOS == "darwin"
}

// -------------------------------------------------------------------------------------
func IsLinux() bool {
	return runtime.GOOS == "linux"
}

//-------------------------------------------------------------------------------------
// 檔案操作
//-------------------------------------------------------------------------------------

func IsFileExists(_fn string) bool {
	_, err := os.Stat(_fn)
	return !os.IsNotExist(err)
}

// -------------------------------------------------------------------------------------
func File2Bytes(_fn string) []byte {
	data, err := os.ReadFile(_fn)
	if err != nil {
		return nil
	}
	return data
}

// -------------------------------------------------------------------------------------
func File2String(_fn string) string {
	return string(File2Bytes(_fn))
}

// -------------------------------------------------------------------------------------
func Bytes2File(_fn string, _content []byte) bool {
	err := os.WriteFile(_fn, _content, 0644)
	return err == nil
}

// -------------------------------------------------------------------------------------
func DeleteFile(_fn string) bool {
	err := os.Remove(_fn)
	return err == nil
}

// -------------------------------------------------------------------------------------
// 網路功能 (HTTP)
// -------------------------------------------------------------------------------------
func HttpGet_BytesData(_url string, _authToken string, _timeoutMs int) []byte {
	_client := &http.Client{
		Timeout: time.Duration(_timeoutMs) * time.Millisecond,
	}
	if _timeoutMs <= 0 {
		_client.Timeout = DefaultTimeout
	}

	_req, _err := http.NewRequest("GET", strings.ReplaceAll(_url, " ", "%20"), nil)
	if _err != nil {
		return nil
	}

	_req.Header.Set("Connection", "close")
	if _authToken != "" {
		_req.Header.Set("Authentication", "Bearer "+_authToken)
	}

	resp, err := _client.Do(_req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	return payload
}

// -------------------------------------------------------------------------------------
func HttpGet(_url string, _authToken string, _timeoutMs int) string {
	_payload := HttpGet_BytesData(_url, _authToken, _timeoutMs)
	if _payload == nil {
		return ""
	}
	return string(_payload)
}

// -------------------------------------------------------------------------------------
func HttpGetAsync(_url string, _callback func([]byte)) {
	go func() {
		_payload := HttpGet_BytesData(_url, "", int(DefaultTimeout.Milliseconds()))
		if _callback != nil {
			_callback(_payload)
		}
	}()
}

// -------------------------------------------------------------------------------------
func HttpPost_BytesData(_url string, _authToken string, _ignoreSSL bool, _contentType string, _content string, _timeoutMs int) []byte {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: _ignoreSSL}, // 關鍵：對應 Java 的 NoopHostnameVerifier
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(_timeoutMs) * time.Millisecond,
	}
	if _timeoutMs <= 0 {
		client.Timeout = DefaultTimeout
	}

	req, err := http.NewRequest("POST", strings.ReplaceAll(_url, " ", "%20"), strings.NewReader(_content))
	if err != nil {
		return nil
	}

	req.Header.Set("Connection", "close")
	if _contentType != "" {
		req.Header.Set("Content-Type", _contentType)
	}
	if _authToken != "" {
		req.Header.Set("Authentication", "Bearer "+_authToken)
		req.Header.Set("Authorization", "Bearer "+_authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	return payload
}

// -------------------------------------------------------------------------------------
func HttpPostWithHeaders(_url string, _headers map[string]string, _contentType string, _content string, _timeoutMs int) string {
	_client := &http.Client{
		Timeout: time.Duration(_timeoutMs) * time.Millisecond,
	}

	_req, _err := http.NewRequest("POST", strings.ReplaceAll(_url, " ", "%20"), strings.NewReader(_content))
	if _err != nil {
		return ""
	}

	if _contentType != "" {
		_req.Header.Set("Content-Type", _contentType)
	}
	for k, v := range _headers {
		_req.Header.Set(k, v)
	}

	_resp, _err := _client.Do(_req)
	if _err != nil {
		return ""
	}
	defer _resp.Body.Close()

	payload, _ := io.ReadAll(_resp.Body)
	return string(payload)
}

// -------------------------------------------------------------------------------------
func HttpPost(_url string, _authToken string, _contentType string, _content string, _timeoutMs int) string {
	return string(HttpPost_BytesData(_url, _authToken, DefaultInsecureTLS, _contentType, _content, _timeoutMs))
}

// -------------------------------------------------------------------------------------
func GetImageFromURL(_imageURL string, _authorization string, _timeoutMs int) []byte {
	if !strings.HasPrefix(_imageURL, "http") {
		return nil
	}

	_client := &http.Client{
		Timeout: time.Duration(_timeoutMs) * time.Millisecond,
	}
	if _timeoutMs <= 0 {
		_client.Timeout = 3000 * time.Millisecond
	}

	_req, _err := http.NewRequest("GET", _imageURL, nil)
	if _err != nil {
		return nil
	}

	if _authorization != "" {
		_req.Header.Set("Authorization", _authorization)
	}

	_resp, _err := _client.Do(_req)
	if _err != nil {
		return nil
	}
	defer _resp.Body.Close()

	_data, _ := io.ReadAll(_resp.Body)
	return _data
}

// -------------------------------------------------------------------------------------
// 網路功能 (Net)
// -------------------------------------------------------------------------------------
func GetLocalMACAddress(_separator string) string {
	defer func() {
		// 捕捉可能發生的異常 (如之前 diag.go 實作的機制)
		if _r := recover(); _r != nil {
			Log.Print(LL_Error, fmt.Sprintf("GetLocalMACAddress Failed: %v", _r))
		}
	}()

	_list := make([]string, 0)

	// 1. 嘗試透過 Go 標準庫獲取
	_interfaces, _err := net.Interfaces()
	if _err == nil {
		for _, _iface := range _interfaces {
			_mac := _iface.HardwareAddr
			// 排除虛擬網卡 (Java 原碼邏輯：第一個位元組不為 00)
			if len(_mac) > 0 && _mac[0] != 0x00 {
				_macStrings := make([]string, len(_mac))
				for _i, _b := range _mac {
					_macStrings[_i] = fmt.Sprintf("%02X", _b)
				}
				_list = append(_list, strings.Join(_macStrings, _separator))
			}
		}

		if len(_list) > 0 {
			sort.Strings(_list)
			return _list[0]
		}
	}

	// 2. 備案：透過指令獲取
	return GetLocalMACAddressByConsoleCMD(_separator)
}

// -------------------------------------------------------------------------------------
// GetLocalMACAddressByConsoleCMD 執行系統指令獲取 MAC
func GetLocalMACAddressByConsoleCMD(_separator string) string {
	var _cmd *exec.Cmd
	// 根據 OS 選擇指令
	if runtime.GOOS == "windows" {
		_cmd = exec.Command("ipconfig", "/all")
	} else {
		_cmd = exec.Command("sh", "-c", "ifconfig -a")
	}

	_stdout, _err := _cmd.StdoutPipe()
	if _err != nil {
		return ""
	}

	if _err := _cmd.Start(); _err != nil {
		return ""
	}

	_reader := bufio.NewReader(_stdout)
	_mac := ""

	for {
		_line, _err := _reader.ReadString('\n')
		if _err != nil {
			break
		}

		// 模擬 Java 的解析邏輯：尋找 HWaddr 或 ether
		if strings.Contains(_line, "HWaddr") || strings.Contains(_line, "ether") {
			_parts := strings.Fields(_line)
			for _i, _p := range _parts {
				if (_p == "HWaddr" || _p == "ether") && _i+1 < len(_parts) {
					_mac = _parts[_i+1]
					break
				}
			}
		}
		if _mac != "" {
			break
		}
	}

	_cmd.Wait()

	// 統一分隔符號與大小寫
	_mac = strings.ReplaceAll(_mac, ":", _separator)
	_mac = strings.ReplaceAll(_mac, "-", _separator)

	return strings.ToUpper(_mac)
}

// -------------------------------------------------------------------------------------
func GetLocalIPv4Address() string {
	_addrs, _err := net.InterfaceAddrs()
	if _err != nil {
		return ""
	}
	for _, _address := range _addrs {
		if _ipnet, _ok := _address.(*net.IPNet); _ok && !_ipnet.IP.IsLoopback() {
			if _ipnet.IP.To4() != nil {
				return _ipnet.IP.String()
			}
		}
	}
	return ""
}

// -------------------------------------------------------------------------------------
func GetInternetIPv4Address() string {
	_url := "https://checkip.amazonaws.com"
	_client := http.Client{
		Timeout: 5 * time.Second,
	}
	_resp, _err := _client.Get(_url)
	if _err != nil {
		return ""
	}
	defer _resp.Body.Close()

	_buf := new(bytes.Buffer)
	_buf.ReadFrom(_resp.Body)
	return strings.TrimSpace(_buf.String())
}

// -------------------------------------------------------------------------------------
// killProcessByPort 根據埠號找出並關閉進程 (支援 Windows, Mac, Linux)
// -------------------------------------------------------------------------------------
func KillProcessByPort(_port int) {
	var _pid string

	if IsMSWindow() {
		// Windows: 透過 netstat 找出佔用該 port 且處於 LISTENING 狀態的 PID
		// 指令範例: netstat -ano | findstr :80 | findstr LISTENING
		_cmd := fmt.Sprintf("netstat -ano | findstr :%d | findstr LISTENING", _port)
		_out := ShellCMDSync(_cmd)
		_lines := strings.Split(strings.TrimSpace(_out), "\n")

		if len(_lines) > 0 && _lines[0] != "" {
			// Windows netstat 輸出格式最後一欄位通常是 PID
			_parts := strings.Fields(_lines[0])
			if len(_parts) > 0 {
				_pid = _parts[len(_parts)-1]
			}
		}

	} else {
		// Linux & macOS: 使用 lsof 直接取得 PID (-t 代表僅輸出 PID)
		_cmd := fmt.Sprintf("lsof -t -i:%d", _port)
		_pid = strings.TrimSpace(ShellCMDSync(_cmd))
	}

	// 如果有找到 PID，則執行殺掉進程的動作
	if _pid != "" && _pid != "0" {
		Log.Print(LL_Info, fmt.Sprintf("Found PID %s occupying port %d. Killing it...", _pid, _port))
		KillProcess(_pid)
	}
}

// -------------------------------------------------------------------------------------
func IsPortInUsing(_port int) bool {
	// 檢查 TCP
	_tcpAddr := fmt.Sprintf(":%d", _port)
	_l, _err := net.Listen("tcp", _tcpAddr)
	if _err != nil {
		return true // 無法監聽代表已被佔用
	}
	_l.Close()

	// 檢查 UDP
	_udpAddr, _err := net.ResolveUDPAddr("udp", _tcpAddr)
	if _err != nil {
		return true
	}
	_pc, _err := net.ListenUDP("udp", _udpAddr)
	if _err != nil {
		return true
	}
	_pc.Close()

	return false
}

// -------------------------------------------------------------------------------------
func IsHostAlive(_host string, _timeout int) bool {
	if _timeout <= 0 {
		_timeout = 2000
	}
	_addr := fmt.Sprintf("%s:80", _host) // 嘗試連接常用端口 80
	_conn, _err := net.DialTimeout("tcp", _addr, time.Duration(_timeout)*time.Millisecond)
	if _err != nil {
		return false
	}
	_conn.Close()
	return true
}

// -------------------------------------------------------------------------------------
func SendMulticastData(_multicastGroup string, _multicastPort int, _data []byte) {
	_addr, _err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", _multicastGroup, _multicastPort))
	if _err != nil {
		return
	}
	_conn, _err := net.DialUDP("udp", nil, _addr)
	if _err != nil {
		return
	}
	defer _conn.Close()
	_conn.Write(_data)
}

// -------------------------------------------------------------------------------------
func SendCommunityText(_vendor, _from, _to, _text string) string {
	_msg := map[string]string{
		"method": "text",
		"to":     _to,
		"text":   _text,
	}
	if _from != "" {
		_msg["from"] = _from
	}

	_json, _err := json.Marshal(_msg)
	if _err != nil {
		return ""
	}

	_url := fmt.Sprintf("https://www.mars-cloud.com:9591/%s/send_text", _vendor)
	return HttpPost(_url, "", "application/json", string(_json), 5000)
}

// -------------------------------------------------------------------------------------
// 命令執行 (Shell CMD)
// -------------------------------------------------------------------------------------
func ShellCMDWithPath(path string, cmds ...string) *exec.Cmd {
	var cmd *exec.Cmd
	fullCmd := strings.Join(cmds, " ")

	// 根據 OS 決定執行殼層
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", fullCmd)
	} else {
		cmd = exec.Command("/bin/sh", "-c", fullCmd)
	}

	if path != "" {
		cmd.Dir = path
	}

	// 預設將錯誤輸出導向到標準輸出，方便同步讀取
	return cmd
}

// -------------------------------------------------------------------------------------
func ShellCMD(cmds ...string) *exec.Cmd {
	return ShellCMDWithPath("", cmds...)
}

// -------------------------------------------------------------------------------------
func ShellCMDSync(_cmds ...string) string {
	var _cmd *exec.Cmd
	_fullCmd := strings.Join(_cmds, " ")

	if IsMSWindow() {
		_cmd = exec.Command("cmd", "/c", _fullCmd)
	} else {
		_cmd = exec.Command("/bin/sh", "-c", _fullCmd)
	}

	_out, _err := _cmd.CombinedOutput()
	if _err != nil {
		return ""
	}
	return string(_out)
}

// -------------------------------------------------------------------------------------
// ShellCMDArgsSync 直接 fork/exec，不經過 /bin/sh 或 cmd /c，避免 user input 注入命令
// 當參數可能含使用者控制資料時，請優先使用此版本而非 ShellCMDSync
func ShellCMDArgsSync(_name string, _args ...string) string {
	_out, _err := exec.Command(_name, _args...).CombinedOutput()
	if _err != nil {
		return ""
	}
	return string(_out)
}

// -------------------------------------------------------------------------------------
type IShellCMDCallback func(proc *os.Process, data string)

// -------------------------------------------------------------------------------------
func ShellCMDAsync(callback IShellCMDCallback, cmds ...string) {
	go func() {
		cmd := ShellCMD(cmds...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return
		}

		if err := cmd.Start(); err != nil {
			return
		}

		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			if callback != nil {
				callback(cmd.Process, line)
			}
		}
		cmd.Wait()
	}()
}

// -------------------------------------------------------------------------------------
// Process 功能
// -------------------------------------------------------------------------------------
func GetPID(cmd *exec.Cmd) int {
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Pid
	}
	return os.Getpid()
}

// -------------------------------------------------------------------------------------
func KillProcess(pid string) bool {
	p, err := strconv.Atoi(pid)
	if err != nil {
		return false
	}

	proc, err := os.FindProcess(p)
	if err != nil {
		return false
	}

	// 在 Unix 下使用 SIGKILL (-9)，Windows 下直接 Kill
	err = proc.Kill()
	return err == nil
}

// -------------------------------------------------------------------------------------
// KillProcessByName 找出與 _name 同名（不含路徑）但 PID 不同於當前進程的所有實例並關閉，回傳實際關閉的數量
// Windows: 比對 IMAGENAME（含 .exe）；Unix: 解析 ps argv[0] 的 basename，避開 Linux comm 被截斷到 15 字元的限制
func KillProcessByName(_name string) int {
	if _name == "" {
		return 0
	}

	_selfPID := os.Getpid()
	_pids := []string{}

	if IsMSWindow() {
		_out := ShellCMDSync(fmt.Sprintf(`tasklist /FI "IMAGENAME eq %s" /NH /FO CSV`, _name))
		for _, _line := range strings.Split(_out, "\n") {
			_line = strings.TrimSpace(_line)
			if _line == "" || strings.HasPrefix(strings.ToUpper(_line), "INFO:") {
				continue
			}
			_fields := strings.Split(_line, ",")
			if len(_fields) < 2 {
				continue
			}
			_pids = append(_pids, strings.Trim(_fields[1], `"`))
		}
	} else {
		_out := ShellCMDSync("ps -A -o pid=,args=")
		_marker := "/" + _name
		for _, _line := range strings.Split(_out, "\n") {
			_line = strings.TrimSpace(_line)
			if _line == "" {
				continue
			}
			_parts := strings.SplitN(_line, " ", 2)
			if len(_parts) < 2 {
				continue
			}
			_pid := _parts[0]
			_argsStr := _parts[1]

			// 主路徑：把 args 第一個 token 視為 argv[0]，比對 basename
			_argv0 := _argsStr
			if _idx := strings.IndexAny(_argv0, " \t"); _idx >= 0 {
				_argv0 = _argv0[:_idx]
			}
			if filepath.Base(_argv0) == _name {
				_pids = append(_pids, _pid)
				continue
			}

			// fallback：argv[0] 路徑含空白時 SplitN 會切錯，改在整段 args 找 "/<name>"（後接空白 / tab / 結尾）
			if strings.HasSuffix(_argsStr, _marker) ||
				strings.Contains(_argsStr, _marker+" ") ||
				strings.Contains(_argsStr, _marker+"\t") {
				_pids = append(_pids, _pid)
			}
		}
	}

	_killed := 0
	for _, _pid := range _pids {
		_p, _err := strconv.Atoi(_pid)
		if _err != nil || _p <= 0 || _p == _selfPID {
			continue
		}
		Log.Print(LL_Info, fmt.Sprintf("Killing duplicate process %s (PID %d)", _name, _p))
		if KillProcess(_pid) {
			_killed++
		}
	}
	return _killed
}

// -------------------------------------------------------------------------------------
// KillSiblingInstance 偵測並關閉與當前執行檔同名的舊實例（避免服務重複啟動）
func KillSiblingInstance() int {
	_self, _err := os.Executable()
	if _err != nil {
		return 0
	}
	return KillProcessByName(filepath.Base(_self))
}

// -------------------------------------------------------------------------------------
func RestartItSelf() error {
	self, err := os.Executable() // 取得當前執行檔路徑
	if err != nil {
		return err
	}

	args := os.Args // 取得啟動時的原始參數

	// 在 Windows 下，如果是透過 cmd 啟動，通常需要開啟新視窗
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "start", self)
		cmd.Args = append(cmd.Args, args[1:]...)
		return cmd.Start()
	}

	// Unix 系統可以直接使用 Fork/Exec
	cmd := exec.Command(self, args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Start()
}

// -------------------------------------------------------------------------------------
// 加密與編碼
// -------------------------------------------------------------------------------------
func BytesToBase64String(_data []byte) string {
	return base64.StdEncoding.EncodeToString(_data)
}

// -------------------------------------------------------------------------------------
func Base64ToBytes(_data_string string) []byte {
	_data, _err := base64.StdEncoding.DecodeString(_data_string)
	if _err != nil {
		return nil
	}
	return _data
}

// -------------------------------------------------------------------------------------
func SHA1ToBytes(_data_string string) []byte {
	_h := sha1.New()
	_h.Write([]byte(_data_string))
	return _h.Sum(nil)
}

// -------------------------------------------------------------------------------------
func Bytes2Hex(_b byte) string {
	return fmt.Sprintf("%02X", _b)
}

// -------------------------------------------------------------------------------------
func Base64ConvertToImageAndSave2Local(_base64String string, _path string) bool {
	// 處理 Data URL 格式 (例如: data:image/png;base64,...)
	_parts := strings.Split(_base64String, ",")
	_actualData := ""
	if len(_parts) > 1 {
		_actualData = _parts[1]
	} else {
		_actualData = _parts[0]
	}

	_data, _err := base64.StdEncoding.DecodeString(_actualData)
	if _err != nil {
		return false
	}

	_err = os.WriteFile(_path, _data, 0644)
	return _err == nil
}

// -------------------------------------------------------------------------------------
// Websocket 處理
// -------------------------------------------------------------------------------------
func DecodeWebClientData(_data []byte, _length int) []byte {
	if _length < 6 {
		return nil
	}

	_offset := 0
	_realIndex := 0
	_tempData := make([]byte, _length)

	for _offset+6 <= _length && _data[_offset] == 0x81 {
		var _subLen int
		_maskOffset := 0

		// 判斷長度欄位
		_payloadLen := int(_data[_offset+1] & 0x7F)
		switch _payloadLen {
		case 126:
			{
				_subLen = (int(_data[_offset+2]) << 8) + int(_data[_offset+3])
				_maskOffset = _offset + 4
				break
			}
		case 127:
			{
				_subLen = 0
				for i := 0; i < 8; i++ {
					_subLen = (_subLen << 8) + int(_data[_offset+2+i])
				}
				_maskOffset = _offset + 10
				break
			}
		default:
			{
				_subLen = _payloadLen
				_maskOffset = _offset + 2
				break
			}
		}

		_mask := _data[_maskOffset : _maskOffset+4]
		dataStart := _maskOffset + 4

		for i := 0; i < _subLen && (dataStart+i) < _length; i++ {
			_tempData[_realIndex] = _data[dataStart+i] ^ _mask[i%4]
			_realIndex++
		}
		_offset = dataStart + _subLen
	}

	return _tempData[:_realIndex]
}

// -------------------------------------------------------------------------------------
func EncodeWebClientData(_data []byte, _length int) []byte {
	var _header []byte
	if _length <= 125 {
		_header = []byte{0x81, byte(_length)}
	} else if _length <= 65535 {
		_header = []byte{0x81, 126, byte((_length >> 8) & 0xFF), byte(_length & 0xFF)}
	} else {
		// 8 byte length header
		_header = make([]byte, 10)
		_header[0] = 0x81
		_header[1] = 127
		for i := 0; i < 8; i++ {
			_header[9-i] = byte((_length >> (i * 8)) & 0xFF)
		}
	}
	return append(_header, _data[:_length]...)
}

// -------------------------------------------------------------------------------------
// 資料處理
// -------------------------------------------------------------------------------------
func ByteArraySearch(_data []byte, _start int, _end int, _comp []byte) int {
	_compLen := len(_comp)
	_searchEnd := _end - _compLen
	if _searchEnd < _start {
		return -1
	}

	for i := _start; i <= _searchEnd; i++ {
		_match := true
		for j := 0; j < _compLen; j++ {
			if _data[i+j] != _comp[j] {
				_match = false
				break
			}
		}
		if _match {
			return i
		}
	}
	return -1
}

// -------------------------------------------------------------------------------------
func IsJSONValid(_src string) bool {
	var _js json.RawMessage
	return json.Unmarshal([]byte(_src), &_js) == nil
}

// -------------------------------------------------------------------------------------
func XValueCompare(_a interface{}, _b interface{}) string {
	if _a == nil || _b == nil {
		return ""
	}

	// 使用反射獲取數值進行比較
	_valA := reflect.ValueOf(_a)
	_valB := reflect.ValueOf(_b)

	// 如果都是數值類型
	if isNumeric(_valA) && isNumeric(_valB) {
		_floatA := toFloat(_valA)
		_floatB := toFloat(_valB)
		if _floatA == _floatB {
			return "="
		} else if _floatA < _floatB {
			return "<"
		} else {
			return ">"
		}
	}

	// 字串比較
	_strA := fmt.Sprintf("%v", _a)
	_strB := fmt.Sprintf("%v", _b)
	if _strA == _strB {
		return "="
	}

	return "!="
}

// -------------------------------------------------------------------------------------
func isNumeric(_v reflect.Value) bool {
	switch _v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// -------------------------------------------------------------------------------------
func toFloat(_v reflect.Value) float64 {
	if _v.Kind() == reflect.Float32 || _v.Kind() == reflect.Float64 {
		return _v.Float()
	}
	if _v.Kind() >= reflect.Int && _v.Kind() <= reflect.Int64 {
		return float64(_v.Int())
	}
	return 0
}

// -------------------------------------------------------------------------------------
// 系統工具
// -------------------------------------------------------------------------------------
func GetFullMachineID() string {
	osType := runtime.GOOS

	// 根據不同 OS 選擇指令
	switch osType {
	case "windows":
		// 先嘗試抓主機板序號
		out, _ := exec.Command("wmic", "baseboard", "get", "serialnumber").Output()
		res := parseWindowsID(string(out))
		if res == "" || strings.Contains(strings.ToLower(res), "none") || strings.Contains(res, ".") {
			// 失敗則抓 CPU ID
			out, _ = exec.Command("wmic", "cpu", "get", "ProcessorId").Output()
			res = parseWindowsID(string(out))
		}
		if res != "" {
			return res
		}
	case "darwin": // MacOS
		out, _ := exec.Command("sh", "-c", "system_profiler SPHardwareDataType | grep Serial").Output()
		parts := strings.Split(string(out), ":")
		if len(parts) >= 2 {
			return strings.ToUpper(strings.TrimSpace(parts[1]))
		}
	case "linux":
		out, _ := exec.Command("sh", "-c", "lshw -c system | grep serial").Output()
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "serial:") {
				res := strings.ReplaceAll(line, "serial:", "")
				res = strings.ToUpper(strings.TrimSpace(res))
				return strings.ReplaceAll(res, ":", "")
			}
		}
	}

	return GetLocalMACAddress("")
}

// -------------------------------------------------------------------------------------
func GetMachineID() string {
	id := GetFullMachineID()
	if len(id) > 0 {
		if len(id) > 6 {
			return id[len(id)-6:]
		}
		return id
	}

	return ""
}

// -------------------------------------------------------------------------------------
func parseWindowsID(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) >= 2 {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(lines[1]), " ", ""))
	}
	return ""
}

// -------------------------------------------------------------------------------------
// 其他工具
// -------------------------------------------------------------------------------------
func ReadLineFromKeyIn() string {
	_reader := bufio.NewReader(os.Stdin)
	_line, _err := _reader.ReadString('\n')
	if _err != nil {
		return ""
	}
	return strings.TrimSpace(_line)
}

// -------------------------------------------------------------------------------------
func ParseURLParams(_paramString string) map[string]string {
	_payload := make(map[string]string)
	if strings.Contains(_paramString, "?") {
		_parts := strings.Split(_paramString, "?")
		if len(_parts) > 1 {
			_paramString = _parts[1]
		}
	}

	_values, _err := url.ParseQuery(_paramString)
	if _err == nil {
		for _key, _val := range _values {
			if len(_val) > 0 {
				_payload[_key] = _val[0]
			}
		}
	}
	return _payload
}

// -------------------------------------------------------------------------------------
func TransformID(_uuid string, _suid string) string {
	if _uuid == "" {
		return ""
	}
	if _suid != "" {
		return _uuid + "_" + _suid
	}
	return _uuid
}

// -------------------------------------------------------------------------------------
func TransformComposedID(_composedID string) []string {
	return strings.Split(_composedID, "_")
}

// -------------------------------------------------------------------------------------
func ForceFlushMemory() {
	runtime.GC()
}

// -------------------------------------------------------------------------------------
// Send Email
// -------------------------------------------------------------------------------------
func dialAndSend(_host, _port, _user, _pass, _to string, _msg []byte, _isSSL bool) bool {
	_addr := _host + ":" + _port
	_auth := smtp.PlainAuth("", _user, _pass, _host)

	if _isSSL {
		// 處理 Port 465 類的隱式 SSL
		_tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         _host,
		}
		_conn, _err := tls.Dial("tcp", _addr, _tlsConfig)
		if _err != nil {
			return false
		}
		defer _conn.Close()

		_client, _err := smtp.NewClient(_conn, _host)
		if _err != nil {
			return false
		}
		defer _client.Quit()

		if _err = _client.Auth(_auth); _err != nil {
			return false
		}
		if _err = _client.Mail(_user); _err != nil {
			return false
		}
		if _err = _client.Rcpt(_to); _err != nil {
			return false
		}

		_w, _err := _client.Data()
		if _err != nil {
			return false
		}
		_, _err = _w.Write(_msg)
		_w.Close()
		return _err == nil
	}

	// 處理標準 SMTP (STARTTLS)
	_err := smtp.SendMail(_addr, _auth, _user, []string{_to}, _msg)
	return _err == nil
}

// -------------------------------------------------------------------------------------
func SendEMail(_sender map[string]interface{}, _to string, _subject string, _contentType string, _content string) bool {
	_account := fmt.Sprintf("%v", _sender["account"])
	_password := fmt.Sprintf("%v", _sender["password"])
	_host := fmt.Sprintf("%v", _sender["host"])
	_port := fmt.Sprintf("%v", _sender["port"])
	_isSSL, _ := _sender["ssl"].(bool)

	if _host == "#none.activated" {
		return true
	}

	if _contentType == "" {
		_contentType = "text/plain; charset=UTF-8"
	}

	// 構建郵件標頭
	_headers := make(map[string]string)
	_headers["From"] = _account
	_headers["To"] = _to
	_headers["Subject"] = "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(_subject)) + "?="
	_headers["MIME-Version"] = "1.0"
	_headers["Content-Type"] = _contentType

	_message := ""
	for _k, _v := range _headers {
		_message += fmt.Sprintf("%s: %s\r\n", _k, _v)
	}
	_message += "\r\n" + _content

	return dialAndSend(_host, _port, _account, _password, _to, []byte(_message), _isSSL)
}

// -------------------------------------------------------------------------------------
func SendEMailAttachment(_sender map[string]interface{}, _to string, _subject string, _msgType string, _msg string, _attType string, _attName string, _buffer []byte) bool {
	_account := fmt.Sprintf("%v", _sender["account"])
	_password := fmt.Sprintf("%v", _sender["password"])
	_host := fmt.Sprintf("%v", _sender["host"])
	_port := fmt.Sprintf("%v", _sender["port"])
	_isSSL, _ := _sender["ssl"].(bool)

	if _msgType == "" {
		_msgType = "text/plain; charset=UTF-8"
	}

	_boundary := "MarsCloudBoundary12345"

	// 郵件主標頭
	_header := fmt.Sprintf("From: %s\r\n", _account)
	_header += fmt.Sprintf("To: %s\r\n", _to)
	_header += "Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(_subject)) + "?=\r\n"
	_header += "MIME-Version: 1.0\r\n"
	_header += fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n", _boundary)
	_header += "\r\n"

	// 內文部分
	_body := fmt.Sprintf("--%s\r\n", _boundary)
	_body += fmt.Sprintf("Content-Type: %s\r\n\r\n", _msgType)
	_body += _msg + "\r\n"

	// 附件部分
	_attachment := fmt.Sprintf("--%s\r\n", _boundary)
	_attachment += fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", _attType, _attName)
	_attachment += "Content-Transfer-Encoding: base64\r\n"
	_attachment += fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", _attName)
	_attachment += "\r\n"
	_attachment += base64.StdEncoding.EncodeToString(_buffer) + "\r\n"
	_attachment += fmt.Sprintf("--%s--", _boundary)

	_fullMessage := _header + _body + _attachment

	return dialAndSend(_host, _port, _account, _password, _to, []byte(_fullMessage), _isSSL)
}

// -------------------------------------------------------------------------------------
// 系統效能監控
// -------------------------------------------------------------------------------------
func GetProcessMemoryUsage() uint32 {
	var _m runtime.MemStats
	runtime.ReadMemStats(&_m)
	return uint32(_m.Sys / 1024 / 1024)
}

// -------------------------------------------------------------------------------------
func GetSystemMemoryState() *mem.VirtualMemoryStat {

	_v, _ := mem.VirtualMemory()

	return _v
}

// -------------------------------------------------------------------------------------
func GetSystemMemoryUsage() uint32 {

	_v := GetSystemMemoryState()
	if _v == nil {
		return 0
	}

	return uint32(_v.Used / 1024 / 1024)
}

// -------------------------------------------------------------------------------------
func GetSystemCPUUsage() float64 {

	_os := runtime.GOOS

	var _out []byte
	var _err error

	switch _os {

	case "windows":
		{
			// Windows 使用 wmic 獲取負載百分比
			_out, _err = exec.Command("wmic", "cpu", "get", "loadpercentage").Output()
			if _err == nil {
				_lines := strings.Split(string(_out), "\n")
				if len(_lines) >= 2 {
					_val := strings.TrimSpace(_lines[1])
					_usage, _ := strconv.ParseFloat(_val, 64)
					return _usage
				}
			}
		}

	case "darwin":
		{
			_out, _err = exec.Command("sh", "-c", "top -l 1 | grep \"CPU usage\" | awk '{print $7}'").Output()
			if _err == nil {

				_idleStr := strings.TrimSpace(string(_out))
				_idleStr = strings.ReplaceAll(_idleStr, "%", "")
				_idleStr = strings.ReplaceAll(_idleStr, ",", ".") // 處理部分系統逗點小數
				_idle, _ := strconv.ParseFloat(_idleStr, 64)

				return 100.0 - _idle
			}
		}

	default:
		{
			// Linux/macOS 透過 top 指令獲取 (簡化處理)
			// 這裡取一個時間點的閒置率來計算
			_out, _err = exec.Command("sh", "-c", "top -bn1 | grep 'Cpu(s)' | awk '{print $8}'").Output()
			if _err == nil {
				_idleStr := strings.TrimSpace(string(_out))
				_idleStr = strings.ReplaceAll(_idleStr, "%", "")
				_idleStr = strings.ReplaceAll(_idleStr, ",", ".") // 處理部分系統逗點小數
				_idle, _ := strconv.ParseFloat(_idleStr, 64)
				return 100.0 - _idle
			}
		}
	}

	return 0.0
}

// -------------------------------------------------------------------------------------
func GetProcessCPUUsage() float64 {
	_os := runtime.GOOS
	_pid := GetPID(nil)
	var _out []byte
	var _err error

	if _os == "windows" {
		// Windows 獲取特定進程 CPU
		_cmdStr := fmt.Sprintf("wmic path Win32_PerfFormattedData_PerfProc_Process where IDProcess=%d get PercentProcessorTime", _pid)
		_out, _err = exec.Command("cmd", "/c", _cmdStr).Output()
		if _err == nil {
			_lines := strings.Split(string(_out), "\n")
			if len(_lines) >= 2 {
				_val := strings.TrimSpace(_lines[1])
				_usage, _ := strconv.ParseFloat(_val, 64)
				return _usage
			}
		}
	} else {
		// Unix-like 使用 ps 指令獲取 %cpu
		_cmdStr := fmt.Sprintf("ps -p %d -o %%cpu | tail -n 1", _pid)
		_out, _err = exec.Command("sh", "-c", _cmdStr).Output()
		if _err == nil {
			_val := strings.TrimSpace(string(_out))
			_usage, _ := strconv.ParseFloat(_val, 64)
			return _usage
		}
	}
	return 0.0
}

// -------------------------------------------------------------------------------------
// 異常監控與處理
// -------------------------------------------------------------------------------------
var (
	_ServiceName                      string
	_UnknowExceptionThreshold         int
	_UnknowExceptionTick              int
	_UnknowExceptionAutoResetCallback func()
	_HandlerEnabled                   bool
)

// -------------------------------------------------------------------------------------
func ExceptionToString(_err interface{}) string {
	// debug.Stack() 對應 Java 的 e.printStackTrace()

	_stack := string(debug.Stack())
	_lines := strings.Split(_stack, "\n")
	_summary := fmt.Sprintf("Panic: %v\n\n", _err)
	_found := false

	for _, _line := range _lines {
		// 去除前後空白與 Tab
		_line = strings.TrimSpace(_line)

		// 檢查是否包含 Go 原始碼路徑標記
		if strings.Contains(_line, ".go:") {
			// 過濾掉不需要顯示的框架或工具層級資訊
			// 1. runtime/：Go 內建運行時
			// 2. sysutil/diag.go：此監控工具本身
			if strings.Contains(_line, "runtime/") || strings.Contains(_line, "sysutil/diag.go") {
				continue
			}

			// Go 的格式通常是 "/路徑/檔案.go:行數 +0x偏移量"
			// 我們只拿第一個空白前的部分（即路徑與行數）
			_parts := strings.Split(_line, " ")
			if len(_parts) > 0 {
				_summary += "  at " + _parts[0] + "\n"
				_found = true
			}
		}
	}

	if !_found {
		return fmt.Sprintf("Panic: %v (未偵測到應用程式原始碼追蹤)", _err)
	}
	return _summary
}

// -------------------------------------------------------------------------------------
func EnableUncaughtExceptionHandler(_serviceName string, _threshold int, _callback func()) {
	_ServiceName = _serviceName
	_UnknowExceptionThreshold = _threshold
	_UnknowExceptionAutoResetCallback = _callback
	_HandlerEnabled = true
}

// -------------------------------------------------------------------------------------
func DisableUncaughtExceptionHandler() {
	_HandlerEnabled = false
}

// -------------------------------------------------------------------------------------
func GlobalRecovery() {

	if !_HandlerEnabled {
		return
	}

	if _r := recover(); _r != nil {
		_UnknowExceptionTick++

		// 構建錯誤訊息
		_errStr := ExceptionToString(_r)
		_logMsg := fmt.Sprintf("Unexpected Error (Tick: %d/%d)\n\n%s",
			_UnknowExceptionTick, _UnknowExceptionThreshold, _errStr)

		fmt.Println("")
		Log.Print(LL_Error, _logMsg)

		// 檢查是否達到重置門檻
		if _UnknowExceptionThreshold > 0 && _UnknowExceptionTick >= _UnknowExceptionThreshold {
			Log.Print(LL_Error, fmt.Sprintf("%s: Too many unknown exceptions, try reset itself ...\n", _ServiceName))

			if _UnknowExceptionAutoResetCallback != nil {
				_UnknowExceptionAutoResetCallback()
			}

			// 呼叫先前在 process.go 實作過的 RestartItSelf
			RestartItSelf()
		}
	}
}

// -------------------------------------------------------------------------------------
func GetMethodName(_class interface{}) string {
	return fmt.Sprintf("%T", _class)
}

// -------------------------------------------------------------------------------------
func SafeRun(_task func()) {
	defer GlobalRecovery()
	_task()
}

// -------------------------------------------------------------------------------------
func If(condition bool, trueVal any, falseVal any) any {
	if condition {
		return trueVal
	}
	return falseVal
}

// -------------------------------------------------------------------------------------
