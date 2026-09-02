package main

import (
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

type SmallTalkService struct {
	Counter      int
	MCPListeners *MCPListenerSet
	stopWorkers  []func()
}

// The MarsService SDK still exposes legacy MQTT callbacks. They intentionally
// remain no-ops because SmallTalk no longer starts or consumes MQTT.
func (s *SmallTalkService) OnMQTTConnected()             {}
func (s *SmallTalkService) OnMQTTConnectionLost(error)   {}
func (s *SmallTalkService) OnMQTTMessage(string, string) {}

func (s *SmallTalkService) Process() {
	Tools.Log.Print(Tools.LL_Info, "SmallTalkService started")
	for {
		s.Counter++
		Tools.Log.Print(Tools.LL_Debug, "[%s] counter=%d", time.Now().Format("15:04:05"), s.Counter)
		time.Sleep(30 * time.Second)
	}
}

func (s *SmallTalkService) OnPropertyChange(property *MarsJSON.JSONObject) {
	_ = property
	Tools.Log.Print(Tools.LL_Debug, "OnPropertyChange")
}

func (s *SmallTalkService) BeforeServiceStop() {
	Tools.Log.Print(Tools.LL_Debug, "BeforeServiceStop")
	if s == nil {
		return
	}
	for i := len(s.stopWorkers) - 1; i >= 0; i-- {
		if s.stopWorkers[i] != nil {
			s.stopWorkers[i]()
		}
	}
	if s.MCPListeners != nil {
		if err := s.MCPListeners.Shutdown(10 * time.Second); err != nil {
			Tools.Log.Print(Tools.LL_Error, "MCP graceful shutdown error: %v", err)
		}
	}
}
