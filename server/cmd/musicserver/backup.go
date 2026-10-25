package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"musicserver/internal/music"
)

func backupCommand(args []string) error {
	if len(args) == 0 || args[0] != "restore" {
		return errors.New("usage: musicserver backup restore --file <backup.tar.gz> --destination <new-empty-directory>")
	}
	flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	file := flags.String("file", "", "complete or database-only backup archive")
	destination := flags.String("destination", "", "new or empty restore directory (never the running library)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *file == "" || *destination == "" {
		return errors.New("--file and --destination are required")
	}
	manifest, err := music.RestoreBackup(context.Background(), *file, *destination)
	if err != nil {
		return err
	}
	fmt.Printf("RESTORE SUCCEEDED: %s (schema %d, %d files, %d originally missing media files)\n", *destination, manifest.Schema, len(manifest.Files), len(manifest.MissingMedia))
	fmt.Println("Old browser sessions are cleared and automatic backups are disabled. Verify media, permissions, deployment configuration and credentials before starting the restored service.")
	return nil
}
