package main

import (
	"embed"
	_ "embed"
	"log"

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
}

func main() {
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
	recMgr := recorder.NewManager()
	v := vault.New(conn.DB)
	dialer := sshconn.New(v, keys, knownHosts)
	pool := sshconn.NewPool(dialer)

	settingsSvc := NewSettingsService(settings, v)
	autoLock := NewAutoLockService(v, settingsSvc)
	autoLock.Start()
	pfSvc := NewPortForwardService(pool, hosts, forwards)

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
			application.NewService(NewExecService(pool, hosts)),
			application.NewService(NewMetricsService(pool, hosts)),
			application.NewService(NewLogsService(pool, hosts)),
			application.NewService(NewAIService(settingsSvc)),
			application.NewService(autoLock),
			application.NewService(pfSvc),
			application.NewService(NewRecordingService(recordings, settings)),
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
