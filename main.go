package main

import (
	"context"
	"embed"
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/adrg/xdg"
	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/service"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var iconData []byte

func init() {
	application.RegisterEvent[service.TerminalData]("terminal:data")
	application.RegisterEvent[service.TerminalExit]("terminal:exit")
	application.RegisterEvent[service.ExecProgress]("exec:progress")
	application.RegisterEvent[service.HostMetrics]("metrics:update")
	application.RegisterEvent[service.LogLine]("logs:line")
	application.RegisterEvent[service.AIChunk]("ai:chunk")
	application.RegisterEvent[service.VaultLockEvent]("vault:locked")
	application.RegisterEvent[service.Notification]("notification:toast")
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
	httpRequests := store.NewHTTPRequests(conn.DB)
	teamActivity := store.NewTeamActivities(conn.DB)
	activities := store.NewActivities(conn.DB)
	recMgr := recorder.NewManager()
	v := vault.New(conn.DB)
	dialer := sshconn.New(v, keys, knownHosts)
	pool := sshconn.NewPool(dialer, hosts)

	settingsSvc := service.NewSettingsService(settings, v)
	autoLock := service.NewAutoLockService(v, settingsSvc)
	autoLock.Start()
	pfSvc := service.NewPortForwardService(pool, hosts, forwards)
	notifySvc := service.NewNotificationService(settings)
	activityRec := service.NewActivityRecorder(activities)

	app := application.New(application.Options{
		Name:        "blacknode",
		Description: "Remote infrastructure command platform",
		Icon:        iconData,
		Services: []application.Service{
			application.NewService(service.NewVaultService(v, conn.DB, activityRec)),
			application.NewService(settingsSvc),
			application.NewService(service.NewKeyService(keys, v)),
			application.NewService(service.NewHostService(hosts, knownHosts, v, conn.DB)),
			application.NewService(service.NewLocalShellService(recMgr, recordings, settings)),
			application.NewService(service.NewSSHService(dialer, hosts, recMgr, recordings, settings)),
			application.NewService(service.NewSFTPService(pool, hosts)),
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
			application.NewService(service.NewSyncService(settings, hosts, snippets, httpRequests, teamActivity, v, activityRec)),
			application.NewService(service.NewActivityService(activities)),
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
		DisableResize:    false,
		BackgroundColour: application.NewRGB(8, 8, 11),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})

	if runtime.GOOS == "linux" {
		applyLinuxFullscreenFix := func() {
			time.AfterFunc(50*time.Millisecond, func() {
				screen, err := win.GetScreen()
				if err != nil || screen == nil {
					return
				}
				win.SetSize(screen.WorkArea.Width, screen.WorkArea.Height)
			})
			time.AfterFunc(300*time.Millisecond, func() {
				screen, err := win.GetScreen()
				if err != nil || screen == nil {
					return
				}
				win.SetSize(screen.WorkArea.Width, screen.WorkArea.Height)
			})
		}

		win.OnWindowEvent(events.Common.WindowMaximise, func(_ *application.WindowEvent) {
			applyLinuxFullscreenFix()
		})

		win.OnWindowEvent(events.Common.WindowFullscreen, func(_ *application.WindowEvent) {
			time.AfterFunc(50*time.Millisecond, func() {
				screen, err := win.GetScreen()
				if err != nil || screen == nil {
					return
				}
				win.SetSize(screen.Size.Width, screen.Size.Height)
			})
			time.AfterFunc(300*time.Millisecond, func() {
				screen, err := win.GetScreen()
				if err != nil || screen == nil {
					return
				}
				win.SetSize(screen.Size.Width, screen.Size.Height)
			})
		})
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}

	pfSvc.StopAll(context.Background())
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
