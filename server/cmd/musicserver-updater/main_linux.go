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
	refresh := flag.String("refresh-worker", "", "root maintenance: install the matching signed same-schema updater release only")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected positional arguments")
	}
	installer, err := update.LoadInstaller(*config)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if *refresh != "" {
		if err = installer.RefreshWorker(ctx, *refresh); err != nil {
			log.Fatal(err)
		}
		log.Print("independent updater replaced; application service, current release and database retained")
		return
	}
	unlock, err := update.WorkerLock(installer.Config.State)
	if err != nil {
		log.Fatal(err)
	}
	defer unlock()
	if err = installer.WriteCapabilities(); err != nil {
		log.Fatal(err)
	}
	if err = installer.RunQueued(ctx); err != nil {
		log.Fatal(err)
	}
}
