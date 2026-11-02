package music

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repeat adding an already-present member: membership is unchanged, but all
// retained IDs must still be validated. Song lyrics must not dominate this cost.
func BenchmarkPlaylistMemberValidationLargeLyrics(b *testing.B) {
	root := b.TempDir()
	if err := os.Mkdir(filepath.Join(root, "media"), 0700); err != nil {
		b.Fatal(err)
	}
	s, err := NewStore(filepath.Join(root, "data.sqlite"), filepath.Join(root, "media"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	admin, err := s.InitializeAdmin("admin", "isolated benchmark administrator")
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 1000)
	lyrics := strings.Repeat("lyric line\n", 6554)
	if err := s.transaction(func(tx *sql.Tx) error {
		for i := range ids {
			ids[i] = fmt.Sprintf("10000000-0000-4000-9000-%012d", i)
			if err := writeTrack(tx, Track{ID: ids[i], Title: ids[i], Path: ids[i] + ".mp3", OwnerID: admin.ID, UploadedBy: admin.ID, Version: 1, Lyrics: lyrics}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	if err := s.PutPlaylist(Playlist{ID: "member-bench", Name: "Large lyrics", TrackIDs: ids}, MutationOptions{UserID: admin.ID}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.AddPlaylistTracks("member-bench", ids[:1], MutationOptions{UserID: admin.ID}); err != nil {
			b.Fatal(err)
		}
	}
}
