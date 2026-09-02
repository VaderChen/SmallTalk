package MQTTServer

// -------------------------------------------------------------------------------------
import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"bytes"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Go/Tools"
)

// -------------------------------------------------------------------------------------
type MessageCallback func(_topic string, _payload string)

// -------------------------------------------------------------------------------------
type Config struct {
	Host    string
	TCPPort int
	WSPort  int
	SSLPort int
	WSSPort int

	CertFile string
	KeyFile  string

	OnMessage MessageCallback
}

// -------------------------------------------------------------------------------------
type MQTTServer struct {
	config  Config
	server  *mqtt.Server
	started bool
	lock    sync.Mutex
}

// -------------------------------------------------------------------------------------
func Create(_config Config) *MQTTServer {
	return &MQTTServer{config: _config}
}

// -------------------------------------------------------------------------------------
func (_this *MQTTServer) Start() error {
	_this.lock.Lock()
	defer _this.lock.Unlock()

	if _this.started {
		return nil
	}

	_server := mqtt.New(&mqtt.Options{
		Logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})

	if _err := _server.AddHook(new(auth.AllowHook), nil); _err != nil {
		return _err
	}

	if _this.config.OnMessage != nil {
		if _err := _server.AddHook(&messageHook{callback: _this.config.OnMessage}, nil); _err != nil {
			return _err
		}
	}

	_tlsConfig, _err := _this.loadTLSConfig()
	if _err != nil {
		return _err
	}

	if _this.config.TCPPort > 0 {
		if _err = _server.AddListener(listeners.NewTCP(listeners.Config{
			ID:      "mqtt-tcp",
			Address: buildAddress(_this.config.Host, _this.config.TCPPort),
		})); _err != nil {
			return _err
		}
	}

	if _this.config.WSPort > 0 {
		if _err = _server.AddListener(listeners.NewWebsocket(listeners.Config{
			ID:      "mqtt-ws",
			Address: buildAddress(_this.config.Host, _this.config.WSPort),
		})); _err != nil {
			return _err
		}
	}

	if _tlsConfig != nil && _this.config.SSLPort > 0 {
		if _err = _server.AddListener(listeners.NewTCP(listeners.Config{
			ID:        "mqtt-ssl",
			Address:   buildAddress(_this.config.Host, _this.config.SSLPort),
			TLSConfig: _tlsConfig,
		})); _err != nil {
			return _err
		}
	}

	if _tlsConfig != nil && _this.config.WSSPort > 0 {
		if _err = _server.AddListener(listeners.NewWebsocket(listeners.Config{
			ID:        "mqtt-wss",
			Address:   buildAddress(_this.config.Host, _this.config.WSSPort),
			TLSConfig: _tlsConfig,
		})); _err != nil {
			return _err
		}
	}

	if _tlsConfig == nil && (_this.config.SSLPort > 0 || _this.config.WSSPort > 0) {
		Tools.Log.Print(Tools.LL_Warning, "MQTT TLS listener skipped: mqtt_tls_cert 或 mqtt_tls_key 未設定")
	}

	_this.server = _server
	_this.started = true

	go func() {
		if _serveErr := _server.Serve(); _serveErr != nil {
			Tools.Log.Print(Tools.LL_Error, "MQTT server serve fail: %s", _serveErr.Error())
		}
	}()

	Tools.Log.Print(Tools.LL_Info, "MQTT server started: tcp=%d, ws=%d, ssl=%d, wss=%d",
		_this.config.TCPPort, _this.config.WSPort, _this.config.SSLPort, _this.config.WSSPort)

	return nil
}

// -------------------------------------------------------------------------------------
func (_this *MQTTServer) Close() error {
	_this.lock.Lock()
	defer _this.lock.Unlock()

	if !_this.started || _this.server == nil {
		return nil
	}

	_err := _this.server.Close()
	_this.started = false
	_this.server = nil

	if _err == nil {
		Tools.Log.Print(Tools.LL_Info, "MQTT server stopped")
	}

	return _err
}

// -------------------------------------------------------------------------------------
func (_this *MQTTServer) loadTLSConfig() (*tls.Config, error) {
	_certFile := strings.TrimSpace(_this.config.CertFile)
	_keyFile := strings.TrimSpace(_this.config.KeyFile)

	if _certFile == "" || _keyFile == "" {
		return nil, nil
	}

	_cert, _err := tls.LoadX509KeyPair(_certFile, _keyFile)
	if _err != nil {
		return nil, fmt.Errorf("load mqtt tls cert/key fail: %w", _err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{_cert},
	}, nil
}

// -------------------------------------------------------------------------------------
func buildAddress(_host string, _port int) string {
	_host = strings.TrimSpace(_host)
	if _host == "" || _host == "0.0.0.0" {
		return fmt.Sprintf(":%d", _port)
	}

	return fmt.Sprintf("%s:%d", _host, _port)
}

// -------------------------------------------------------------------------------------
type messageHook struct {
	mqtt.HookBase
	callback MessageCallback
}

// -------------------------------------------------------------------------------------
func (_h *messageHook) ID() string {
	return "message-callback"
}

// -------------------------------------------------------------------------------------
func (_h *messageHook) Provides(_b byte) bool {
	return bytes.Contains([]byte{
		mqtt.OnPublished,
	}, []byte{_b})
}

// -------------------------------------------------------------------------------------
func (_h *messageHook) OnPublished(_cl *mqtt.Client, _pk packets.Packet) {
	if _h.callback == nil {
		return
	}

	_h.callback(_pk.TopicName, string(_pk.Payload))
}
