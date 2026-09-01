package main

import (
	"context"
	"embed"
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/service"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var iconData []byte

func init() {
	if os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}

	application.RegisterEvent[service.TerminalData]("terminal:data")
	application.RegisterEvent[service.TerminalExit]("terminal:exit")
	application.RegisterEvent[service.ExecProgress]("exec:progress")
	application.RegisterEvent[service.HostMetrics]("metrics:update")
	application.RegisterEvent[service.LogLine]("logs:line")
	application.RegisterEvent[service.AIChunk]("ai:chunk")
	application.RegisterEvent[service.VaultLockEvent]("vault:locked")
	application.RegisterEvent[service.Notification]("notification:toast")
	application.RegisterEvent[service.AuthPromptRequest]("auth:prompt")
	application.RegisterEvent[service.TransferProgress]("sftp:progress")
}

func main() {
	closeLog := setupFileLogger()
	defer closeLog()

	conn, err := db.Open()
	if err != nil {
		log.Fatalf("db open: %v", err)
	}

	hosts := store.NewHosts(conn.DB)
	keys := store.NewKeys(conn.DB)
	knownHosts := store.NewKnownHosts(conn.DB)
	secrets := store.NewSecrets(conn.DB)
	settings := store.NewSettings(conn.DB)
	forwards := store.NewForwards(conn.DB)
	recordings := store.NewRecordings(conn.DB)
	snippets := store.NewSnippets(conn.DB)
	history := store.NewHistory(conn.DB)
	logQueries := store.NewLogQueries(conn.DB)
	dbConnections := store.NewDBConnections(conn.DB)
	httpRequests := store.NewHTTPRequests(conn.DB)
	teamActivity := store.NewTeamActivities(conn.DB)
	syncKeys := store.NewSyncKeys(conn.DB)
	activities := store.NewActivities(conn.DB)
	recMgr := recorder.NewManager()
	v := vault.New(conn.DB)
	dialer := sshconn.New(v, keys, knownHosts, secrets)
	// The prompter has to be attached before the pool takes the dialer:
	// without it the dialer cannot offer keyboard-interactive, which is how
	// every MFA-protected host expects to be authenticated.
	authPrompt := service.NewAuthPromptService()
	dialer.Prompter = authPrompt
	pool := sshconn.NewPool(dialer, hosts)

	settingsSvc := service.NewSettingsService(settings, v)
	autoLock := service.NewAutoLockService(v, settingsSvc)
	autoLock.Start()
	pfSvc := service.NewPortForwardService(pool, hosts, forwards)
	notifySvc := service.NewNotificationService(settings)
	activityRec := service.NewActivityRecorder(activities)
	syncSvc := service.NewSyncService(settings, hosts, snippets, httpRequests, teamActivity, syncKeys, v, activityRec)
	dataDir := filepath.Join(xdg.DataHome, "blacknode")
	vaultSvc := service.NewVaultService(v, conn.DB, dataDir, activityRec, autoLock)
	service.WireSyncService(vaultSvc, syncSvc)

	app := application.New(application.Options{
		Name:        "blacknode",
		Description: "Remote infrastructure command platform",
		Icon:        iconData,
		Services: []application.Service{
			application.NewService(vaultSvc),
			application.NewService(settingsSvc),
			application.NewService(service.NewKeyService(keys, v)),
			application.NewService(service.NewHostService(hosts, knownHosts, secrets, v)),
			application.NewService(service.NewLocalShellService(recMgr, recordings, settings, dialer)),
			application.NewService(service.NewSSHService(dialer, hosts, recMgr, recordings, settings)),
			application.NewService(service.NewSFTPService(pool, hosts)),
			application.NewService(authPrompt),
			application.NewService(service.NewExecService(pool, hosts, history, notifySvc, activityRec)),
			application.NewService(service.NewMetricsService(pool, hosts, notifySvc)),
			application.NewService(service.NewLogsService(pool, hosts, logQueries)),
			application.NewService(service.NewAIService(settingsSvc)),
			application.NewService(autoLock),
			application.NewService(pfSvc),
			application.NewService(service.NewRecordingService(recordings, settings)),
			application.NewService(service.NewContainerService(pool, hosts)),
			application.NewService(service.NewSnippetService(snippets, history)),
			application.NewService(service.NewHistoryService(history)),
			application.NewService(service.NewNetworkService(pool, hosts)),
			application.NewService(service.NewProcessService(pool, hosts)),
			application.NewService(service.NewHTTPService(pool, hosts, httpRequests)),
			application.NewService(service.NewDBService(pool, hosts, dbConnections, v)),
			application.NewService(notifySvc),
			application.NewService(service.NewUpdateService()),
			application.NewService(service.NewPluginService(notifySvc, activityRec)),
			application.NewService(syncSvc),
			application.NewService(service.NewActivityService(activities)),
			// New services — Feature 1: Autocomplete
			application.NewService(service.NewAutocompleteService(history, snippets)),
			// New services — Feature 2: Mosh
			application.NewService(service.NewMoshService(hosts)),
			// Legacy console protocols: Telnet + Serial
			application.NewService(service.NewTelnetService(hosts)),
			application.NewService(service.NewSerialService(hosts)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			ProgramName: "blacknode",
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Blacknode",
		Width:            1280,
		Height:           820,
		MinWidth:         800,
		MinHeight:        500,
		StartState:       application.WindowStateMaximised,
		DisableResize:    false,
		BackgroundColour: application.NewRGB(8, 8, 11),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})

	_ = win

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}

	pfSvc.StopAll(context.Background())
	syncSvc.StopAutoSync()
	pool.Close()
	close(autoLock.StopChan)
	log.Printf("=== blacknode stop ===")
}

func setupFileLogger() func() {
	dir := filepath.Join(xdg.DataHome, "blacknode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, "blacknode.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("=== blacknode start ===")
	return func() { _ = f.Close() }
}
