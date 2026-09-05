package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/HttpService"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsJSON"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/MarsService"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Security"
	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Tools"
)

const (
	defaultLobbyProjectID = "default"
	defaultLobbyProject   = "Default"
	defaultLobbyRoomID    = "lobby"
	defaultLobbyRoomName  = "Lobby 大廳"
)

func RunService() {
	cloud := &SmallTalkService{processStop: make(chan struct{})}
	service := MarsService.Create("agent.properties", cloud)
	marsCloudURL := service.Property.OptString("mars_cloud_url", "")
	defaultAccount := service.Property.OptString("default_Account", "root")
	defaultPassword := service.Property.OptString("default_password", "")
	defaultProj := service.Property.OptString("mars_cloud_proj", "")
	webEntryPath := service.Property.OptString("web_entry_path", "/talk.html")

	dataDir := service.Property.OptString("data_dir", "./data")
	initPersistentJWSKeys(dataDir)
	maxMsgs := service.Property.OptInt("max_inmem_messages", 200)
	persist := service.Property.OptBoolean("persist_messages", true)

	boardsExportDir := service.Property.OptString("boards_export_dir", service.Property.OptString("rooms_export_dir", "./boards"))
	boardsExportIntervalSec := service.Property.OptInt("boards_export_interval_sec", service.Property.OptInt("rooms_export_interval_sec", 180))

	mcpHTTPPort := service.Property.OptInt("mcp_http_port", 18792)
	mcpHTTPSPort := service.Property.OptInt("mcp_https_port", 0)
	var store *Store
	var storeErr error

	postgresEnabled := service.Property.OptBoolean("postgres_enabled", true)
	if !postgresEnabled {
		Tools.Log.Print(Tools.LL_Info, "PostgreSQL disabled by configuration; using disk/in-memory store")
	} else if pgStore, pgErr := ConnectLocalPostgres(); pgErr == nil {
		Tools.Log.Print(Tools.LL_Info, "Local PostgreSQL connected successfully, loading data directly from SQL (no disk files read)...")
		store, storeErr = NewStoreWithPostgres(pgStore, maxMsgs)
		if storeErr != nil {
			Tools.Log.Print(Tools.LL_Error, "PostgreSQL store initialization failed: %v (falling back to disk)", storeErr)
			store = nil
		} else {
			Tools.Log.Print(Tools.LL_Info, "PostgreSQL mode active: all data loaded and managed via PostgreSQL dedicated tables")
		}
	} else {
		var profileErr *ProfileSchemaError
		if errors.As(pgErr, &profileErr) {
			Tools.Log.Print(Tools.LL_Error, "帳號名稱資料升級失敗，停止啟動: %v", pgErr)
			return
		}
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
	var emailManager *EmailManager
	emailKey, emailPepper, emailSecretErr := LoadOrCreateEmailSecrets(dataDir)
	if emailSecretErr != nil {
		Tools.Log.Print(Tools.LL_Error, "email verification secret initialization failed: %v", emailSecretErr)
		return
	}
	publicBaseURL := service.Property.OptString("email_public_base_url", "https://bbs.mars-cloud.com")
	dailyRegistrationLimit := service.Property.OptInt("email_daily_registration_limit", defaultDailyRegistrationLimit)
	resendAPIKeyEnv := strings.TrimSpace(service.Property.OptString("resend_api_key_env", "RESEND_API_KEY"))
	resendAPIKey := ""
	if resendAPIKeyEnv != "" {
		resendAPIKey = strings.TrimSpace(os.Getenv(resendAPIKeyEnv))
	}
	resendFrom := strings.TrimSpace(service.Property.OptString("email_from", ""))
	if resendFrom == "" {
		resendFrom = strings.TrimSpace(os.Getenv("SMALLTALK_EMAIL_FROM"))
	}
	var emailSender EmailSender
	if resendAPIKey != "" && resendFrom != "" {
		emailSender = &ResendEmailSender{APIKey: resendAPIKey, From: resendFrom}
	}
	emailManager, storeErr = NewEmailManager(store, dataDir, publicBaseURL, emailKey, emailPepper, emailSender)
	if storeErr != nil {
		Tools.Log.Print(Tools.LL_Error, "email verification initialization failed: %v", storeErr)
		return
	}
	if err := emailManager.ConfigureRegistration(service.Property.OptString("email_registration_mode", registrationModeStandard), dailyRegistrationLimit); err != nil {
		Tools.Log.Print(Tools.LL_Error, "invalid Email registration configuration: %v", err)
		return
	}
	if !emailManager.Available() {
		// 註冊仍依模式處理，實際寄送另由全站額度保護。
		Tools.Log.Print(Tools.LL_Info, "Email verification is configured but delivery is disabled until environment variable %q and email_from are set", resendAPIKeyEnv)
	}
	if err := emailManager.ConfigureEmailLimit(service.Property.OptInt("email_daily_send_limit", 100)); err != nil {
		Tools.Log.Print(Tools.LL_Error, "invalid Email delivery configuration: %v", err)
		return
	}
	store.SetDefaultAdminPassword(defaultPassword)
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
	// 新帳號依 EmailManager 的標準／嚴格模式核准與核發 TOKEN；停用舊的
	// 定時自動核准 worker，既有帳號與已核發 TOKEN 不受影響。
	if store.AutoApprovalEnabled() {
		if err := store.SetAutoApprovalEnabled(false); err != nil {
			Tools.Log.Print(Tools.LL_Error, "disable legacy auto approval failed: %v", err)
			return
		}
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
		Email:           emailManager,
	}

	facade := &SmallTalkFacade{Store: store, Email: emailManager}
	mcpHandler := NewMCPHTTPHandler(facade)
	service.AddRestfulAPI("/mcp", &mcpRestfulCallback{handler: mcpHandler})
	service.AddRestfulAPI("/auth", authAPI)
	service.AddRestfulAPI("/permissions", &PermissionsAPI{Store: store, Email: emailManager})
	service.AddRestfulAPI("/api", &BBSAPI{Store: store, Facade: facade})
	if service.HttpService != nil {
		service.HttpService.SetDefaultHTML(webEntryPath)
		service.HttpService.SetDefaultCacheControl("public, max-age=300")
	}
	service.RegistryServerInfo(GetVersionTag(), "pack", true)
	cloud.MCPListeners = startMCPListeners(store, mcpHTTPPort, mcpHTTPSPort, service.Property.OptString("ssl_key", ""), service.Property.OptString("ssl_key_file", ""), emailManager)
	service.Start()
	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	cloud.stopWorkers = append(cloud.stopWorkers, func() {
		shutdownOnce.Do(func() { close(shutdown) })
	})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-shutdown:
				return
			case <-ticker.C:
				if service.MarsClient != nil {
					authAPI.setMarsClient(service.MarsClient)
					return
				}
			}
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
			if count, err := store.PruneVisitorMessages(ttl); err != nil {
				Tools.Log.Print(Tools.LL_Error, "Prune expired visitor messages failed (%s): %v", reason, err)
			} else if count > 0 {
				Tools.Log.Print(Tools.LL_Info, "Pruned %d expired messages from visitors room (%s, TTL: %v)", count, reason, ttl)
			}
		}
		pruneVisitors("on startup")
		for {
			select {
			case <-shutdown:
				return
			case <-ticker.C:
				pruneVisitors("hourly cycle")
			}
		}
	}()

	restartRequests, stopRestartScheduler := startSelfRestartScheduler(
		propertyStrings(service.Property, "smalltalk_restart_time"),
		service.Property.OptString("restart_timezone", ""),
	)
	if stopRestartScheduler != nil {
		cloud.stopWorkers = append(cloud.stopWorkers, stopRestartScheduler)
	}

	select {
	case <-shutdown:
		return
	case target := <-restartRequests:
		Tools.Log.Print(Tools.LL_Warning, "SmallTalk self-restart time reached: %s", target)
		if err := validateSelfRestart(); err != nil {
			Tools.Log.Print(Tools.LL_Error, "Self-restart preflight failed: %v", err)
			// Keep serving when the replacement cannot be prepared. An operator or
			// external supervisor can repair the executable without an avoidable
			// outage; the next process start will arm the schedule again.
			<-shutdown
			return
		}
		service.StopService()
		if err := restartCurrentProcess(); err != nil {
			Tools.Log.Print(Tools.LL_Error, "Self-restart handoff failed: %v", err)
			os.Exit(1)
		}
		// Windows starts a replacement process instead of replacing this image.
		os.Exit(0)
	}
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
		if _, updateErr := store.UpdateRoom(defaultLobbyProjectID, "visitors", "訪客專區/Guest", "公開", "【訪客專區】開放所有人與訪客免登入自由留言，所有留言將於 15 天後自動清除。", "system"); updateErr != nil {
			Tools.Log.Print(Tools.LL_Error, "update visitors room error: %v", updateErr)
		}
	} else if err != nil {
		Tools.Log.Print(Tools.LL_Error, "ensure visitors room error: %v", err)
	}

	Tools.Log.Print(Tools.LL_Info, "Default lobby ready: %s/%s (%s)", defaultLobbyProjectID, defaultLobbyRoomID, defaultLobbyRoomName)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	if err := ensureServerWorkingDir(); err != nil {
		fmt.Fprintf(os.Stderr, "SmallTalk startup failed: %v\n", err)
		return
	}

	Tools.EnableUncaughtExceptionHandler("SmallTalk Service", 3, func() {
		Tools.Log.Print(Tools.LL_Error, "System Error")
	})
	Tools.Log.SetDisplayLevel(Tools.LL_Info)

	RunService()
}

func ensureServerWorkingDir() error {
	target := detectServerDir()
	if target == "" {
		return nil
	}
	current, err := os.Getwd()
	if err == nil && samePath(current, target) {
		return nil
	}
	if err := os.Chdir(target); err != nil {
		return fmt.Errorf("cannot change working directory to %s: %w", target, err)
	}
	return nil
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

func startMCPListeners(store *Store, httpPort, httpsPort int, certFile, keyFile string, emailManagers ...*EmailManager) *MCPListenerSet {
	listeners := &MCPListenerSet{}
	if httpPort <= 0 && httpsPort <= 0 {
		Tools.Log.Print(Tools.LL_Info, "MCP endpoint disabled")
		return listeners
	}
	var emailManager *EmailManager
	if len(emailManagers) > 0 {
		emailManager = emailManagers[0]
	}
	handler := NewMCPHTTPHandler(&SmallTalkFacade{Store: store, Email: emailManager})
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)
	newServer := func(addr string) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      120 * time.Second,
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

func initPersistentJWSKeys(dataDir string) {
	if dataDir == "" {
		dataDir = "./data"
	}
	_ = os.MkdirAll(dataDir, 0755)

	pubPath := filepath.Join(dataDir, "jws_rsa.pub")
	priPath := filepath.Join(dataDir, "jws_rsa.pri")

	// If keys already exist in ./cert, use them
	if _, err := os.Stat("./cert/rsa.pub"); err == nil {
		if _, err := os.Stat("./cert/rsa.pri"); err == nil {
			pubPath = "./cert/rsa.pub"
			priPath = "./cert/rsa.pri"
		}
	}

	if Security.JWT.LoadRSAKeyFromFile(pubPath, priPath) {
		Tools.Log.Print(Tools.LL_Info, "Persistent JWS RSA Key is ready (%s, %s)", pubPath, priPath)
	} else {
		Tools.Log.Print(Tools.LL_Warning, "Persistent JWS RSA Key initialization warning (%s, %s)", pubPath, priPath)
	}
}
