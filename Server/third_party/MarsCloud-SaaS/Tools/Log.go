package Tools

import (
	"fmt"
	"net"
	"os"
	"time"
)

// -------------------------------------------------------------------------------------
// 日誌系統 (簡化版 LogOut)
// -------------------------------------------------------------------------------------
var Log = createLogger()

// -------------------------------------------------------------------------------------
type LogLevel int

// -------------------------------------------------------------------------------------
const (
	LL_Debug LogLevel = iota
	LL_Normal
	LL_Info
	LL_Warning
	LL_Error
)

// -------------------------------------------------------------------------------------
type LogOut struct {
	_RemoteLoggerHost string
	_RemoteLoggerPort int
	_Log_ShowLevel    LogLevel
	_Log_ShowColor    bool
	_UDPConn          *net.UDPConn
}

// -------------------------------------------------------------------------------------
func createLogger() *LogOut {
	return &LogOut{
		_Log_ShowLevel:    LL_Debug,
		_Log_ShowColor:    true,
		_RemoteLoggerPort: 8729,
	}
}

// -------------------------------------------------------------------------------------
func (_this *LogOut) SetRemoteOutput(_enable bool, _addr string) {
	if !_enable {
		if _this._UDPConn != nil {
			_this._UDPConn.Close()
			_this._UDPConn = nil
		}
		return
	}
	_this._RemoteLoggerHost = _addr
	_raddr, _err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", _this._RemoteLoggerHost, _this._RemoteLoggerPort))
	if _err == nil {
		_this._UDPConn, _ = net.DialUDP("udp", nil, _raddr)
	}
}

// -------------------------------------------------------------------------------------
func (_this *LogOut) Print(_maxLevel LogLevel, _msg string, _params ...any) {

	if _maxLevel < _this._Log_ShowLevel {
		return
	}

	if len(_params) > 0 {
		_msg = fmt.Sprintf(_msg, _params...)
	}

	_timeStr := time.Now().Format("01/02 15:04:05")
	_levelStr := ""
	_colorCode := ""

	switch _maxLevel {
	case LL_Normal:
		_levelStr, _colorCode = "[Normal]", "\033[32m"
	case LL_Info:
		_levelStr, _colorCode = "[Info]", "\033[36m"
	case LL_Warning:
		_levelStr, _colorCode = "[Warn]", "\033[33m"
	case LL_Error:
		_levelStr, _colorCode = "[Error]", "\033[35m"
	default:
		_levelStr, _colorCode = "[Debug]", "\033[37m"
	}

	_out := fmt.Sprintf("[%s]%s %s", _timeStr, _levelStr, _msg)

	if _this._Log_ShowColor {
		fmt.Printf("%s%s\033[0m\n", _colorCode, _out)
	} else {
		fmt.Println(_out)
	}

	// 如果有設定遠端輸出，則發送 UDP 封包
	if _this._UDPConn != nil {
		_pid := os.Getpid()
		_remoteMsg := fmt.Sprintf("%d@%s", _pid, _out)
		_this._UDPConn.Write([]byte(_remoteMsg))
	}
}

// -------------------------------------------------------------------------------------
func (_this *LogOut) SetDisplayLevel(_maxLevel LogLevel) {
	_this._Log_ShowLevel = _maxLevel
}

// -------------------------------------------------------------------------------------
func ConsolePrint(_msg string) {
	Log.Print(LL_Info, _msg)
}
