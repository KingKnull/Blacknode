package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/service"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

func readPrivateFile(name string) (string, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", errors.New("credential file must be a regular file with mode 0600 and at most 4096 bytes")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	defer func() {
		for i := range data {
			data[i] = 0
		}
	}()
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", errors.New("credential file is empty")
	}
	return value, nil
}
func run() error {
	database := flag.String("database", "", "runner SQLite database (separate from the desktop database)")
	listen := flag.String("listen", "127.0.0.1:9250", "HTTP address; non-loopback requires TLS")
	tokenFile := flag.String("token-file", "", "private file containing an administrator token of at least 32 characters")
	passwordFile := flag.String("vault-password-file", "", "private file containing the runner vault passphrase")
	seedFile := flag.String("seed", "", "optional JSON configuration exported from Blacknode")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file")
	tlsKey := flag.String("tls-key", "", "TLS key file")
	flag.Parse()
	if *database == "" || *tokenFile == "" || *passwordFile == "" {
		return errors.New("--database, --token-file and --vault-password-file are required")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) && (*tlsCert == "" || *tlsKey == "") {
		return errors.New("non-loopback listeners require --tls-cert and --tls-key")
	}
	token, err := readPrivateFile(*tokenFile)
	if err != nil {
		return err
	}
	password, err := readPrivateFile(*passwordFile)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(*database), 0700); err != nil {
		return err
	}
	conn, err := db.OpenPath(*database)
	if err != nil {
		return err
	}
	defer conn.Close()
	v := vault.New(conn.DB)
	initialized, err := v.IsInitialized()
	if err != nil {
		return err
	}
	if initialized {
		err = v.Unlock(password)
	} else {
		err = v.Setup(password)
	}
	password = ""
	if err != nil {
		return err
	}
	defer v.Lock()
	settings := store.NewSettings(conn.DB)
	cfg := service.NewSettingsService(settings, v)
	hosts := store.NewHosts(conn.DB).WithGroups(store.NewHostGroups(conn.DB))
	known := store.NewKnownHosts(conn.DB)
	dialer := sshconn.New(v, store.NewKeys(conn.DB), known, store.NewSecrets(conn.DB))
	pool := sshconn.NewPool(dialer, hosts)
	defer pool.Close()
	documents := store.NewOperations(conn.DB)
	notify := service.NewNotificationService(settings)
	executor := service.NewExecService(pool, hosts, store.NewHistory(conn.DB), notify, service.NewActivityRecorder(store.NewActivities(conn.DB)))
	metrics := service.NewMetricsService(pool, hosts, notify)
	service.WireMetricsOperations(metrics, documents)
	sftp := service.NewSFTPService(pool, hosts)
	ops := service.NewOpsConsoleService(pool, hosts, service.NewActivityRecorder(store.NewActivities(conn.DB)))
	platform := service.NewPlatformService(documents, hosts, pool, executor, metrics, ops, sftp, cfg, store.NewRecordings(conn.DB))
	books := service.NewRunbookService(settings, executor, documents)
	jobs := service.NewJobService(documents, books, cfg)
	infra := service.NewInfrastructureService(platform, jobs, books, notify)
	api := service.NewRunnerHTTP(infra, known)
	defer api.Close()
	if err = api.Bootstrap(token); err != nil {
		return err
	}
	token = ""
	if *seedFile != "" {
		data, err := os.ReadFile(*seedFile)
		if err != nil {
			return err
		}
		if len(data) > 2<<20 {
			return errors.New("seed exceeds 2 MiB")
		}
		var seed service.RunnerSeed
		if err = json.Unmarshal(data, &seed); err != nil {
			return err
		}
		if err = api.ImportSeed(context.Background(), seed); err != nil {
			return err
		}
	}
	// Scheduling and probes have process lifetimes, independent of desktop views.
	jobs.StartScheduler()
	defer jobs.StopAll()
	infra.Start()
	defer infra.Stop()
	if err = metrics.Restore(context.Background()); err != nil {
		return err
	}
	defer metrics.StopAll(context.Background())
	server := &http.Server{Addr: *listen, Handler: api, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	stopped, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		if *tlsCert != "" {
			done <- server.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			done <- server.ListenAndServe()
		}
	}()
	log.Printf("Blacknode runner listening on %s", *listen)
	select {
	case err = <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-stopped.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
