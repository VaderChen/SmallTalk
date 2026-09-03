package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/HttpService"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsService"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

const (
	defaultLobbyProjectID = "default"
	defaultLobbyProject   = "Default"
	defaultLobbyRoomID    = "lobby"
	defaultLobbyRoomName  = "Lobby 大廳"
)

func RunService() {
	cloud := &SmallTalkService{Counter: 0}
	service := MarsService.Create("agent.properties", cloud)
	marsCloudURL := service.Property.OptString("mars_cloud_url", "")
	defaultAccount := service.Property.OptString("default_Account", "root")
	defaultPassword := service.Property.OptString("default_password", "root")
	defaultProj := service.Property.OptString("mars_cloud_proj", "")
	webEntryPath := service.Property.OptString("web_entry_path", "/talk.html")

	dataDir := service.Property.OptString("data_dir", "./data")
	maxMsgs := service.Property.OptInt("max_inmem_messages", 200)
	persist := service.Property.OptBoolean("persist_messages", true)

	boardsExportDir := service.Property.OptString("boards_export_dir", service.Property.OptString("rooms_export_dir", "./boards"))
	boardsExportIntervalSec := service.Property.OptInt("boards_export_interval_sec", service.Property.OptInt("rooms_export_interval_sec", 180))

	mcpHTTPPort := service.Property.OptInt("mcp_http_port", 18792)
	mcpHTTPSPort := service.Property.OptInt("mcp_https_port", 0)
	var store *Store
	var storeErr error

	if pgStore, pgErr := ConnectLocalPostgres(); pgErr == nil {
		Tools.Log.Print(Tools.LL_Info, "Local PostgreSQL connected successfully, loading data directly from SQL (no disk files read)...")
		store, storeErr = NewStoreWithPostgres(pgStore, maxMsgs)
		if storeErr != nil {
			Tools.Log.Print(Tools.LL_Error, "PostgreSQL store initialization failed: %v (falling back to disk)", storeErr)
			store = nil
		} else {
			Tools.Log.Print(Tools.LL_Info, "PostgreSQL mode active: all data loaded and managed via PostgreSQL dedicated tables")
		}
	} else {
		Tools.Log.Print(Tools.LL_Info, "Local PostgreSQL not connected: %v (using disk/in-memory fallback)", pgErr)
	}

	if store == nil {
		if service.Property.OptBoolean("migrate_legacy_boards", false) {
			if err := MigrateLegacyBoards(dataDir); err != nil {
				Tools.Log.Print(Tools.LL_Error, "legacy board migration failed: %v", err)
				return
			}
		}
		store, storeErr = NewStoreWithError(dataDir, maxMsgs, persist)
		if storeErr != nil {
			Tools.Log.Print(Tools.LL_Error, "store startup failed: %v", storeErr)
			return
		}
	}
	if err := store.ConfigureSecurity(propertyStrings(service.Property, "mcp_allowed_origins"), propertyStrings(service.Property, "trusted_proxy_cidrs")); err != nil {
		Tools.Log.Print(Tools.LL_Error, "invalid security configuration: %v", err)
		return
	}
	ensureDefaultLobby(store)
	if stop := StartRoomSnapshotter(store, boardsExportDir, time.Duration(boardsExportIntervalSec)*time.Second); stop != nil {
		cloud.stopWorkers = append(cloud.stopWorkers, stop)
	}

	memoryHubURL := service.Property.OptString("memoryhub_url", "")
	hourlyEnabled := service.Property.OptBoolean("hourly_summary_enabled", true)
	hourlyInterval := time.Duration(service.Property.OptInt("hourly_summary_interval_sec", 3600)) * time.Second
	if hourlyEnabled {
		if stop := StartHourlySummarizer(store, memoryHubURL, hourlyInterval); stop != nil {
			cloud.stopWorkers = append(cloud.stopWorkers, stop)
		}
	}
	if stop := StartAutoApprovalWorker(store); stop != nil {
		cloud.stopWorkers = append(cloud.stopWorkers, stop)
	}
	metricsCollector := StartSystemMetricsCollector(dataDir)
	cloud.stopWorkers = append(cloud.stopWorkers, func() {
		metricsCollector.Stop()
	})
	authAPI := &HttpAPI_auth{
		MarsCloudURL:    marsCloudURL,
		DefaultAccount:  defaultAccount,
		DefaultPassword: defaultPassword,
		DefaultProj:     defaultProj,
		WebEntryPath:    webEntryPath,
		MarsClient:      service.MarsClient,
		Store:           store,
	}

	facade := &SmallTalkFacade{Store: store}
	mcpHandler := NewMCPHTTPHandler(facade)
	service.AddRestfulAPI("/mcp", &mcpRestfulCallback{handler: mcpHandler})
	service.AddRestfulAPI("/auth", authAPI)
	service.AddRestfulAPI("/permissions", &PermissionsAPI{Store: store})
	service.AddRestfulAPI("/api", &BBSAPI{Store: store, Facade: facade})
	if service.HttpService != nil {
		service.HttpService.SetDefaultHTML(webEntryPath)
		service.HttpService.SetDefaultCacheControl("public, max-age=300")
	}
	service.RegistryServerInfo(GetVersionTag(), "pack", true)
	cloud.MCPListeners = startMCPListeners(store, mcpHTTPPort, mcpHTTPSPort, service.Property.OptString("ssl_key", ""), service.Property.OptString("ssl_key_file", ""))
	service.Start()
	go func() {
		for {
			if service.MarsClient != nil {
				authAPI.MarsClient = service.MarsClient
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		pruneVisitors := func(reason string) {
			if !store.VisitorTTLEnabled() {
				return
			}
			ttl := store.VisitorTTL()
			if count, err := store.PruneVisitorMessages(ttl); err == nil && count > 0 {
				Tools.Log.Print(Tools.LL_Info, "Pruned %d expired messages from visitors room (%s, TTL: %v)", count, reason, ttl)
			}
		}
		pruneVisitors("on startup")
		for range ticker.C {
			pruneVisitors("hourly cycle")
		}
	}()

	select {}
}

func propertyStrings(property *MarsJSON.JSONObject, key string) []string {
	if property == nil {
		return nil
	}
	array := property.OptJSONArray(key)
	if array == nil {
		return nil
	}
	out := make([]string, 0, array.Length())
	for i := 0; i < array.Length(); i++ {
		if value := strings.TrimSpace(array.OptString(i, "")); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func ensureDefaultLobby(store *Store) {
	if store == nil {
		return
	}

	if _, err := store.CreateProject(defaultLobbyProjectID, defaultLobbyProject); err != nil && err != ErrAlreadyExists {
		Tools.Log.Print(Tools.LL_Error, "ensure default lobby project error: %v", err)
		return
	}
	if _, err := store.CreateRoom(defaultLobbyProjectID, defaultLobbyRoomID, defaultLobbyRoomName, "閒聊", "【大廳】所有 agent 與使用者的共同討論區", "system"); err != nil && err != ErrAlreadyExists {
		Tools.Log.Print(Tools.LL_Error, "ensure default lobby room error: %v", err)
		return
	}
	if _, err := store.CreateRoom(defaultLobbyProjectID, "visitors", "訪客專區/Guest", "公開", "【訪客專區】開放所有人與訪客免登入自由留言，所有留言將於 15 天後自動清除。", "system"); err != nil && err == ErrAlreadyExists {
		_, _ = store.UpdateRoom(defaultLobbyProjectID, "visitors", "訪客專區/Guest", "公開", "【訪客專區】開放所有人與訪客免登入自由留言，所有留言將於 15 天後自動清除。", "system")
	} else if err != nil {
		Tools.Log.Print(Tools.LL_Error, "ensure visitors room error: %v", err)
	}

	Tools.Log.Print(Tools.LL_Info, "Default lobby ready: %s/%s (%s)", defaultLobbyProjectID, defaultLobbyRoomID, defaultLobbyRoomName)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	ensureServerWorkingDir()

	Tools.EnableUncaughtExceptionHandler("SmallTalk Service", 3, func() {
		Tools.Log.Print(Tools.LL_Error, "System Error")
	})
	Tools.Log.SetDisplayLevel(Tools.LL_Info)

	RunService()
}

func ensureServerWorkingDir() {
	target := detectServerDir()
	if target == "" {
		return
	}
	current, err := os.Getwd()
	if err == nil && samePath(current, target) {
		return
	}
	_ = os.Chdir(target)
}

func detectServerDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if fileExists(filepath.Join(dir, "agent.properties")) {
			return dir
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		dir := filepath.Dir(file)
		for candidate := dir; candidate != filepath.Dir(candidate); candidate = filepath.Dir(candidate) {
			if fileExists(filepath.Join(candidate, "agent.properties")) {
				return candidate
			}
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b
}

func startMCPListeners(store *Store, httpPort, httpsPort int, certFile, keyFile string) *MCPListenerSet {
	listeners := &MCPListenerSet{}
	if httpPort <= 0 && httpsPort <= 0 {
		Tools.Log.Print(Tools.LL_Info, "MCP endpoint disabled")
		return listeners
	}
	handler := NewMCPHTTPHandler(&SmallTalkFacade{Store: store})
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	newServer := func(addr string) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		}
	}
	if httpPort > 0 {
		server := newServer(fmt.Sprintf("0.0.0.0:%d", httpPort))
		listeners.servers = append(listeners.servers, server)
		go func() {
			Tools.Log.Print(Tools.LL_Info, "MCP HTTP listen at %s", server.Addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				Tools.Log.Print(Tools.LL_Error, "MCP HTTP error at %s: %v", server.Addr, err)
			}
		}()
	}
	if httpsPort > 0 {
		if strings.TrimSpace(certFile) == "" || strings.TrimSpace(keyFile) == "" {
			Tools.Log.Print(Tools.LL_Error, "MCP HTTPS disabled: ssl_key and ssl_key_file are required")
			return listeners
		}
		server := newServer(fmt.Sprintf("0.0.0.0:%d", httpsPort))
		listeners.servers = append(listeners.servers, server)
		go func() {
			Tools.Log.Print(Tools.LL_Info, "MCP HTTPS listen at %s", server.Addr)
			if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
				Tools.Log.Print(Tools.LL_Error, "MCP HTTPS error at %s: %v", server.Addr, err)
			}
		}()
	}
	return listeners
}

type mcpRestfulCallback struct {
	handler http.Handler
}

func (c *mcpRestfulCallback) Process(w http.ResponseWriter, r *http.Request, _ *MarsJSON.JSONObject, _ []string, _ *MarsJSON.JSONObject, body string) []byte {
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	c.handler.ServeHTTP(w, r)
	return []byte(HttpService.ResponseHandledMarker)
}
