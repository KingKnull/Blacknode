package main

import (
	"embed"
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[TerminalData]("terminal:data")
	application.RegisterEvent[TerminalExit]("terminal:exit")
	application.RegisterEvent[ExecProgress]("exec:progress")
	application.RegisterEvent[HostMetrics]("metrics:update")
	application.RegisterEvent[LogLine]("logs:line")
	application.RegisterEvent[AIChunk]("ai:chunk")
	application.RegisterEvent[VaultLockEvent]("vault:locked")
	application.RegisterEvent[Notification]("notification:toast")
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
	settings := store.NewSettings(conn.DB)
	forwards := store.NewForwards(conn.DB)
	recordings := store.NewRecordings(conn.DB)
	snippets := store.NewSnippets(conn.DB)
	history := store.NewHistory(conn.DB)
	logQueries := store.NewLogQueries(conn.DB)
	dbConnections := store.NewDBConnections(conn.DB)
	recMgr := recorder.NewManager()
	v := vault.New(conn.DB)
	dialer := sshconn.New(v, keys, knownHosts)
	pool := sshconn.NewPool(dialer, hosts)

	settingsSvc := NewSettingsService(settings, v)
	autoLock := NewAutoLockService(v, settingsSvc)
	autoLock.Start()
	pfSvc := NewPortForwardService(pool, hosts, forwards)
	notifySvc := NewNotificationService(settings)

	app := application.New(application.Options{
		Name:        "blacknode",
		Description: "Remote infrastructure command platform",
		Services: []application.Service{
			application.NewService(NewVaultService(v)),
			application.NewService(settingsSvc),
			application.NewService(NewKeyService(keys, v)),
			application.NewService(NewHostService(hosts)),
			application.NewService(NewLocalShellService(recMgr, recordings, settings)),
			application.NewService(NewSSHService(dialer, hosts, recMgr, recordings, settings)),
			application.NewService(NewSFTPService(pool, hosts)),
			application.NewService(NewExecService(pool, hosts, history, notifySvc)),
			application.NewService(NewMetricsService(pool, hosts, notifySvc)),
			application.NewService(NewLogsService(pool, hosts, logQueries)),
			application.NewService(NewAIService(settingsSvc)),
			application.NewService(autoLock),
			application.NewService(pfSvc),
			application.NewService(NewRecordingService(recordings, settings)),
			application.NewService(NewContainerService(pool, hosts)),
			application.NewService(NewSnippetService(snippets, history)),
			application.NewService(NewHistoryService(history)),
			application.NewService(NewNetworkService(pool, hosts)),
			application.NewService(NewProcessService(pool, hosts)),
			application.NewService(NewHTTPService(pool, hosts)),
			application.NewService(NewDBService(pool, hosts, dbConnections, v)),
			application.NewService(notifySvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Blacknode",
		Width:            1280,
		Height:           820,
		BackgroundColour: application.NewRGB(8, 8, 11),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupFileLogger tees stderr-style log output to <data-dir>/blacknode.log so
// startup errors are recoverable on Windows where the GUI subsystem hides the
// console. The returned closer flushes and closes the file.
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
