//go:build linux

package main

import (
	"context"
	"flag"
	"log"
	"musicserver/internal/update"
	"time"
)

func main() {
	config := flag.String("config", "/etc/musicserver/updater.json", "root-owned updater configuration")
	flag.Parse()
	installer, err := update.LoadInstaller(*config)
	if err != nil {
		log.Fatal(err)
	}
	unlock, err := update.WorkerLock(installer.Config.State)
	if err != nil {
		log.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err = installer.RunQueued(ctx); err != nil {
		log.Fatal(err)
	}
}
