package music

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestRound3HighFrequencyReadersDoNotQueueOnWriter(t *testing.T) {
	for _, kind := range []string{"cover", "audit", "stats", "preview"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := testAPI(t)
			s := a.store
			track := s.Tracks()[0]
			if err := s.SetTrackCover(track.ID, tinyPNG()); err != nil {
				t.Fatal(err)
			}
			track, _ = s.Track(track.ID)
			stage, err := s.StageAudio("reader.wav", bytes.NewReader(validTestAudio(t)), nil)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := s.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "cover":
					_, ok := s.Cover(track.CoverID)
					if !ok {
						err = errors.New("cover missing")
					}
				case "audit":
					_, err = s.Audit(10, 0)
				case "stats":
					err = a.readCallStats().err
				case "preview":
					_, ok, e := s.UploadStage(stage.ID)
					err = e
					if !ok && err == nil {
						err = errors.New("preview missing")
					}
				}
				done <- err
			}()
			select {
			case err := <-done:
				conn.Close()
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				conn.Close()
				<-done
				t.Fatal(kind + " queued on sole writer connection")
			}
		})
	}
}
