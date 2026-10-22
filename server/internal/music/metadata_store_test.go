package music

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnusedAPIKeyDoesNotExposeZeroDates(t *testing.T) {
	encoded, err := json.Marshal(publicAPIKey(APIKey{ID: "unused", Name: "未使用"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "revoked_at") || strings.Contains(string(encoded), "last_used_at") || strings.Contains(string(encoded), "expires_at") {
		t.Fatalf("zero dates change UI key state: %s", encoded)
	}
}

func TestScanUsesEmbeddedTagsAndLyricsPriority(t *testing.T) {
	api, root := testAPI(t)
	path := filepath.Join(root, "Artist - Song.mp3")
	if err := os.WriteFile(path, testID3AudioMetadata(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	track := api.store.Tracks()[0]
	if track.Title != "中文歌名" || track.Artist != "中文歌手" || track.Album != "标签专辑" || track.Genre != "流行" || !track.EmbeddedLyrics || track.LyricsSource != "ID3v2/USLT" {
		t.Fatalf("embedded tags missing: %+v", track)
	}
	if err := os.WriteFile(filepath.Join(root, "Artist - Song.lrc"), []byte("[00:01]Sidecar"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	track = api.store.Tracks()[0]
	if track.Lyrics != "[00:01]Sidecar" || track.LyricsSource != "sidecar" || !track.EmbeddedLyrics {
		t.Fatalf("sidecar priority: %+v", track)
	}
	if err := api.store.UpdateTrack(track.ID, map[string]string{"artist": "人工歌手", "lyrics": "人工歌词"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	track = api.store.Tracks()[0]
	if track.Artist != "人工歌手" || track.Lyrics != "人工歌词" || track.LyricsSource != "manual" || !track.EmbeddedLyrics {
		t.Fatalf("manual priority: %+v", track)
	}
}

func TestMissingAlbumDoesNotUseDirectory(t *testing.T) {
	api, _ := testAPI(t)
	if track := api.store.Tracks()[0]; track.Album != "" || track.Genre != "" {
		t.Fatalf("directory became a tag: %+v", track)
	}
}

func TestUploadPreservesTagsAndCommitsMetadataOnce(t *testing.T) {
	api, _ := testAPI(t)
	before := api.store.Revision()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "different-filename.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(testID3AudioMetadata())
	_ = form.WriteField("artist", "上传修订歌手")
	_ = form.Close()
	request := httptest.NewRequest("POST", "/api/v2/uploads", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	authorizeTestAdmin(api, request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var track Track
	if err := json.Unmarshal(response.Body.Bytes(), &track); err != nil {
		t.Fatal(err)
	}
	if track.Title != "中文歌名" || track.Artist != "上传修订歌手" || track.Album != "标签专辑" || !track.EmbeddedLyrics {
		t.Fatalf("upload tags overwritten: %+v", track)
	}
	if api.store.Revision() != before+2 {
		t.Fatal("upload metadata was committed separately")
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	rescanned, _ := api.store.Track(track.ID)
	if rescanned.Title != track.Title || rescanned.Artist != track.Artist || rescanned.Lyrics != track.Lyrics {
		t.Fatalf("rescan changed upload: %+v", rescanned)
	}
}

func TestUploadFailureRemovesFileAndRestoresLibrary(t *testing.T) {
	api, root := testAPI(t)
	before := api.store.Revision()
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_upload BEFORE INSERT ON tracks BEGIN SELECT RAISE(ABORT,'reject upload'); END"); err != nil {
		t.Fatal(err)
	}
	_, err := api.store.ImportAudioWithMetadata("failed.mp3", bytes.NewReader(testID3AudioMetadata()), map[string]string{"artist": "手工"})
	if err == nil {
		t.Fatal("upload succeeded with closed database")
	}
	if api.store.Revision() != before || len(api.store.Tracks()) != 1 {
		t.Fatal("failed upload changed the library")
	}
	entries, err := os.ReadDir(filepath.Join(root, "uploads"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed upload left files: %v, %v", entries, err)
	}
}

func TestUntaggedUploadRetainsOriginalNameAfterReopen(t *testing.T) {
	api, root := testAPI(t)
	track, err := api.store.ImportAudio("原始标题 - 原始歌手.wav", bytes.NewReader(validTestAudio(t)))
	if err != nil {
		t.Fatal(err)
	}
	dataPath := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataPath, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	got, exists := store.Track(track.ID)
	if !exists || got.Title != "原始标题" || got.Artist != "原始歌手" || got.Album != "" {
		t.Fatalf("original name lost after restart: %+v", got)
	}
}
