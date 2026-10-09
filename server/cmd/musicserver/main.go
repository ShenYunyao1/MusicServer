package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"musicserver/internal/music"
	"musicserver/internal/update"
	"musicserver/internal/version"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	mediaRoot := flag.String("media-root", "./media", "music directory")
	dataPath := flag.String("data", "./data/library.sqlite", "SQLite library database path (.json paths are unsupported)")
	apiKey := flag.String("api-key", os.Getenv("MUSIC_SERVER_API_KEY"), "optional API key")
	showAPIKey := flag.Bool("show-api-key", false, "print the configured startup admin API key in the log")
	noScan := flag.Bool("no-scan", false, "do not scan on startup")
	flag.Parse()
	if err := os.MkdirAll(*mediaRoot, 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := music.NewStore(*dataPath, *mediaRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if err := store.CleanupUploadStages(); err != nil {
		log.Printf("clean expired uploads: %v", err)
	}
	stopCleanup := make(chan struct{})
	defer close(stopCleanup)
	go func() {
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
		if _, err := store.Scan(); err != nil {
			log.Printf("initial scan skipped: %v", err)
		}
	}
	handler := music.NewAPI(store, *apiKey)
	updater := update.NewManager(filepath.Join(filepath.Dir(store.DataPath()), "updates"), func() string {
		return store.Settings().UpdateRepository
	})
	handler.SetUpdater(updater)
	updateContext, stopUpdates := context.WithCancel(context.Background())
	defer stopUpdates()
	updater.Start(updateContext)
	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	log.Printf("music server v%s listening on %s, media root %s, SQLite database %s", version.Version, *address, filepath.Clean(*mediaRoot), store.DataPath())
	if *apiKey != "" {
		if *showAPIKey {
			log.Printf("ADMIN API KEY (sensitive): %s", *apiKey)
		} else {
			log.Printf("admin authentication enabled by startup API key")
		}
	} else if len(store.APIKeys()) == 0 {
		log.Printf("WARNING: no API key configured and no persistent keys exist; all API requests are currently unauthenticated")
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(fmt.Errorf("listen: %w", err))
	}
}
