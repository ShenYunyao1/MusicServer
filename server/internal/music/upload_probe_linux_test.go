//go:build linux

package music

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStagingProbesAudioOnceAndReusesTechnicalMetadata(t *testing.T) {
	api, _ := testAPI(t)
	directory := t.TempDir()
	counter := filepath.Join(directory, "probe-count")
	script := filepath.Join(directory, "ffprobe-fixture")
	source := "#!/bin/sh\nprintf x >> \"$MUSIC_TEST_PROBE_COUNTER\"\nprintf '%s' '{\"streams\":[{\"codec_type\":\"audio\",\"sample_rate\":\"8000\",\"channels\":1,\"duration\":\"1.0\"}]}'\n"
	if err := os.WriteFile(script, []byte(source), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_TEST_PROBE_COUNTER", counter)
	t.Setenv("MUSIC_SERVER_FFPROBE", script)
	settings := api.store.Settings()
	settings.FFprobePath = script
	if err := api.store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	preview, err := api.store.StageAudio("one-probe.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(counter)
	if err != nil || string(data) != "x" {
		t.Fatalf("audio probed more than once: %q %v", data, err)
	}
	if preview.Track.DurationMS != 1000 || preview.Track.SampleRate != 8000 {
		t.Fatal("validated metadata not reused")
	}
	if _, ok := preview.Timings["probe_ms"]; !ok {
		t.Fatal("probe timing missing")
	}
}
