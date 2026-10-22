package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"musicserver/internal/music"
	"musicserver/internal/update"
	"musicserver/internal/version"
)

func main() {
	defaults, err := executableDefaults()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := adminCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	address := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	mediaRoot := flag.String("media-root", defaults.MediaRoot, "music directory (relative paths follow executable directory)")
	dataPath := flag.String("data", defaults.Data, "schema 4 SQLite database (relative paths follow executable directory)")
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
	if err := ensureAdministrator(store); err != nil {
		log.Fatal(err)
	}
	if err := store.BeginServer(); err != nil {
		log.Fatal(err)
	}
	if err := store.CleanupUploadStages(); err != nil {
		log.Printf("clean expired uploads: %v", err)
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
				if err := store.CleanupUploadStages(); err != nil {
					log.Printf("clean expired uploads: %v", err)
				}
			case <-stopCleanup:
				return
			}
		}
	}()
	if !*noScan {
		if _, err := store.StartScan(false); err != nil {
			log.Printf("initial scan skipped: %v", err)
		}
	}
	handler := music.NewAPI(store)
	handler.SetAuthConfig(authConfig)
	updater := update.NewManager(filepath.Join(filepath.Dir(store.DataPath()), "updates"), func() string {
		return store.Settings().UpdateRepository
	}, store.Backup)
	defer updater.Close()
	handler.SetUpdater(updater)
	updateContext, stopUpdates := context.WithCancel(context.Background())
	defer stopUpdates()
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
	log.Printf("Open http://%s/ in your browser. Press Ctrl+C to stop.", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("listen: %v", err)
		stopSignals()
	}
	close(listenDone)
	<-serverDone
}
