//go:build !linux

package main

import "log"

func main() { log.Fatal("automatic installation is supported by the Linux systemd updater") }
