package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"musicserver/internal/music"
)

func backupCommand(args []string) error {
	if len(args) > 0 && args[0] == "cleanup" {
		flags := flag.NewFlagSet("backup cleanup", flag.ContinueOnError)
		parent := flags.String("parent", "", "parent directory containing abandoned restore staging directories")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *parent == "" {
			return errors.New("--parent is required")
		}
		removed, active, err := music.CleanupRestoreStaging(*parent)
		if err != nil {
			return err
		}
		fmt.Printf("RESTORE CLEANUP: %d staging directories removed; %d locked or inaccessible directories skipped. Zero-byte lock files are retained.\n", removed, active)
		return nil
	}
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
	fmt.Printf("RESTORE SUCCEEDED: %s (schema %d, %d files, %d missing or changed media files)\n", *destination, manifest.Schema, len(manifest.Files), len(manifest.MissingMedia))
	fmt.Println("Old browser sessions are cleared and automatic backups are disabled. Verify media, permissions, deployment configuration and credentials before starting the restored service.")
	return nil
}
