package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"musicserver/internal/activity"
	"musicserver/internal/music"
	"musicserver/internal/update"
	"musicserver/internal/version"
)

func main() {
	defaults, err := executableDefaults()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		if err := backupCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := adminCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	address := flag.String("addr", "0.0.0.0:8080", "HTTP listen address")
	mediaRoot := flag.String("media-root", defaults.MediaRoot, "music directory (relative paths follow executable directory)")
	dataPath := flag.String("data", defaults.Data, "schema 7 SQLite database (relative paths follow executable directory)")
	publicURL := flag.String("public-url", os.Getenv("MUSIC_SERVER_PUBLIC_URL"), "HTTPS origin for remote deployment, e.g. https://music.example.com")
	noScan := flag.Bool("no-scan", false, "do not scan on startup")
	flag.Parse()
	*mediaRoot = resolveRuntimePath(defaults.MediaRoot, *mediaRoot)
	*dataPath = resolveRuntimePath(defaults.MediaRoot, *dataPath)
	authConfig, err := music.NewAuthConfig(*address, *publicURL)
	if err != nil {
		log.Fatal(err)
	}
	unlock, err := music.LockDatabaseProcess(*dataPath)
	if err != nil {
		log.Fatal(err)
	}
	defer unlock()
	if err := os.MkdirAll(*mediaRoot, 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := music.NewStore(*dataPath, *mediaRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	journal, err := activity.Open(filepath.Join(filepath.Dir(store.DataPath()), "logs"))
	if err != nil {
		log.Fatal(err)
	}
	defer journal.Close()
	defer journal.Emit("server.stop", activity.Fields{})
	store.SetActivity(journal)
	_ = journal.Emit("server.start", activity.Fields{})
	handler := music.NewAPI(store)
	handler.SetAuthConfig(authConfig)
	if err := handler.ConfigureWebSetup(); err != nil {
		log.Fatal(err)
	}
	if !handler.SetupPending() {
		if err := store.BeginServer(); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Printf("First startup: open the web page to initialize admin. Authorization file: %s", filepath.Join(filepath.Dir(store.DataPath()), "setup-token"))
	}
	maintenance := func() bool {
		path := os.Getenv("MUSIC_SERVER_UPDATE_MAINTENANCE")
		if path == "" {
			return false
		}
		_, err := os.Stat(path)
		return !os.IsNotExist(err)
	}
	backgroundBlocked := func() bool { return handler.SetupPending() || maintenance() }
	stopBackups := store.StartBackupScheduler(backgroundBlocked)
	defer stopBackups()
	if err := func() error {
		if backgroundBlocked() {
			return nil
		}
		return store.CleanupUploadStages()
	}(); err != nil {
		log.Printf("clean expired uploads: %v", err)
		_ = journal.Emit("server.cleanup_failed", activity.Fields{})
	}
	stopCleanup := make(chan struct{})
	cleanupDone := make(chan struct{})
	defer func() { close(stopCleanup); <-cleanupDone }()
	go func() {
		defer close(cleanupDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if backgroundBlocked() {
					continue
				}
				if err := store.CleanupUploadStages(); err != nil {
					log.Printf("clean expired uploads: %v", err)
					_ = journal.Emit("server.cleanup_failed", activity.Fields{})
				}
			case <-stopCleanup:
				return
			}
		}
	}()
	deferredScan := !*noScan && backgroundBlocked()
	if !*noScan && !deferredScan {
		if _, err := store.StartScan(false); err != nil {
			log.Printf("initial scan skipped: %v", err)
		}
	}
	handler.SetMaintenance(maintenance)
	updater := update.NewManager(filepath.Join(filepath.Dir(store.DataPath()), "updates"), func() string {
		repo := store.Settings().UpdateRepository
		if repo == "" {
			return update.Repository
		}
		return repo
	})
	updater.ConfigureInstaller(os.Getenv("MUSIC_SERVER_UPDATE_QUEUE"), os.Getenv("MUSIC_SERVER_UPDATE_STATE"))
	defer updater.Close()
	handler.SetUpdater(updater)
	updateContext, stopUpdates := context.WithCancel(context.Background())
	defer stopUpdates()
	deferredScanDone := make(chan struct{})
	if deferredScan {
		go func() {
			defer close(deferredScanDone)
			if waitForMaintenance(updateContext, backgroundBlocked) {
				if _, err := store.StartScan(false); err != nil {
					log.Printf("deferred startup scan: %v", err)
				}
			}
		}()
	} else {
		close(deferredScanDone)
	}
	defer func() { stopUpdates(); <-deferredScanDone }()
	updater.Start(updateContext)
	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	serverDone := make(chan struct{})
	listenDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		select {
		case <-signalContext.Done():
			store.CancelScan()
			stopUpdates()
			shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				log.Printf("HTTP shutdown: %v", err)
				_ = server.Close()
			}
		case <-listenDone:
		}
	}()
	commit, modified := version.Source()
	log.Printf("music server v%s (commit %s, modified=%t) listening on %s, media root %s, SQLite database %s", version.Version, commit, modified, *address, filepath.Clean(*mediaRoot), store.DataPath())
	if authConfig.LANHTTP {
		_, port, _ := net.SplitHostPort(*address)
		log.Printf("LAN HTTP enabled. Open http://127.0.0.1:%s/ locally or http://<LAN-IP>:%s/ on your LAN. Press Ctrl+C to stop.", port, port)
	} else {
		log.Printf("Open http://%s/ in your browser. Press Ctrl+C to stop.", *address)
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("listen: %v", err)
		stopSignals()
	}
	close(listenDone)
	<-serverDone
}

func waitForMaintenance(ctx context.Context, blocked func() bool) bool {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if !blocked() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
