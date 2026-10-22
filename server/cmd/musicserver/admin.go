package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"

	"musicserver/internal/music"

	"golang.org/x/term"
)

var readConsolePassword = func() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("password entry requires an interactive terminal")
	}
	fmt.Fprint(os.Stderr, "Password (12+ characters): ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	defer clear(first)
	fmt.Fprint(os.Stderr, "Confirm password: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	defer clear(second)
	if !bytes.Equal(first, second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

func adminCommand(args []string) error {
	if len(args) == 0 || (args[0] != "init" && args[0] != "reset-password") {
		return errors.New("usage: musicserver admin init|reset-password [--data <database>] [--username admin] [--revoke-keys]")
	}
	defaults, err := executableDefaults()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
	data := flags.String("data", defaults.Data, "account database (default beside executable in data directory)")
	username := flags.String("username", "admin", "administrator username")
	revoke := flags.Bool("revoke-keys", false, "also revoke this account's player keys on password reset")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	*data = resolveRuntimePath(defaults.MediaRoot, *data)
	unlock, err := music.LockDatabaseProcess(*data)
	if err != nil {
		return err
	}
	defer unlock()
	store, err := music.NewStore(*data, defaults.MediaRoot)
	if err != nil {
		return err
	}
	defer store.Close()
	if args[0] == "init" && store.RequireAdministrator() == nil {
		return errors.New("administrator already initialized")
	}
	password, err := readConsolePassword()
	if err != nil {
		return err
	}
	if args[0] == "init" {
		_, err = store.InitializeAdmin(*username, password)
	} else {
		err = store.ResetPassword(*username, password, *revoke)
	}
	if err == nil {
		fmt.Fprintln(os.Stderr, "Administrator operation completed.")
	}
	return err
}
