package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestReleaseBuildRejectsWrongTarget(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "GOOS", Value: "windows"}, {Key: "GOARCH", Value: "amd64"}, {Key: "vcs.revision", Value: "commit"}, {Key: "vcs.modified", Value: "false"}}}
	if err := validateReleaseBuild(info, "linux", "amd64", "commit"); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatal("wrong OS accepted", err)
	}
	if err := validateReleaseBuild(info, "windows", "arm64", "commit"); err == nil {
		t.Fatal("wrong arch accepted")
	}
	if err := validateReleaseBuild(info, "windows", "amd64", "commit"); err != nil {
		t.Fatal(err)
	}
}
