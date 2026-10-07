package music

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadStagePreviewCommitAndDedup(t *testing.T) {
	api, _ := testAPI(t)
	preview, err := api.store.StageAudio("preview.mp3", bytes.NewReader(testID3AudioMetadata()), map[string]string{"title": "预览标题"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ID == "" || preview.Track.Title != "预览标题" || preview.ExpiresAt.IsZero() {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	track, duplicate, err := api.store.CommitUploadStage(preview.ID)
	if err != nil || duplicate || track.Title != "预览标题" {
		t.Fatalf("commit failed: track=%+v duplicate=%v err=%v", track, duplicate, err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	rescanned, _ := api.store.Track(track.ID)
	if rescanned.Title != "预览标题" || rescanned.LyricsSource != "ID3v2/USLT" {
		t.Fatalf("upload edits or lyrics source lost on scan: %+v", rescanned)
	}
	second, err := api.store.StageAudio("duplicate.mp3", bytes.NewReader(testID3AudioMetadata()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.DuplicateOf != track.ID {
		t.Fatalf("duplicate was not reported: %+v", second)
	}
	duplicateTrack, duplicate, err := api.store.CommitUploadStage(second.ID)
	if err != nil || !duplicate || duplicateTrack.ID != track.ID {
		t.Fatalf("duplicate commit: track=%+v duplicate=%v err=%v", duplicateTrack, duplicate, err)
	}
	if err := api.store.DeleteUploadStage(second.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted stage should be absent, got %v", err)
	}
}

func TestUploadSidecarsPersistAcrossReopenAndScan(t *testing.T) {
	api, root := testAPI(t)
	before := api.store.Revision()
	preview, err := api.store.stageAudioWithAssets("original.mp3", bytes.NewReader(testID3AudioMetadata()), nil, uploadAssets{Lyrics: []byte("[00:01]External lyrics"), Cover: tinyPNG()})
	if err != nil {
		t.Fatal(err)
	}
	if api.store.Revision() != before || preview.Track.LyricsSource != "sidecar" || !preview.Track.EmbeddedLyrics || preview.Track.CoverSource != "manual" {
		t.Fatalf("unexpected resource preview: %+v", preview)
	}
	asset, err := api.store.UploadStageCover(preview.ID)
	if err != nil || !bytes.Equal(asset.Data, tinyPNG()) {
		t.Fatalf("staged artwork missing: %v", err)
	}
	track, _, err := api.store.commitUploadStageWithMetadata(preview.ID, map[string]string{"title": "Confirmed edit"})
	if err != nil {
		t.Fatal(err)
	}
	path := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Scan(); err != nil {
		t.Fatal(err)
	}
	stored, _ := reopened.Track(track.ID)
	if stored.Title != "Confirmed edit" || stored.Lyrics != "[00:01]External lyrics" || stored.LyricsSource != "sidecar" || stored.CoverSource != "manual" || stored.CoverID != track.CoverID {
		t.Fatalf("resources lost after restart/rescan: %+v", stored)
	}
	if _, ok := reopened.Cover(track.CoverID); !ok {
		t.Fatal("cover bytes did not persist")
	}
	newAPI := NewAPI(reopened, "secret")
	response := callAPI(newAPI, "GET", "/api/v1/covers/"+track.CoverID, "")
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), tinyPNG()) || !strings.Contains(response.Header().Get("Cache-Control"), "private") {
		t.Fatalf("cover HTTP response: %d %v", response.Code, response.Header())
	}
	_, deniedKey := createPersistentKey(t, newAPI, APIKeyCreateRequest{Name: "no-grants", Scopes: []string{ScopeMediaRead}})
	if denied := callAPIWithKey(newAPI, "GET", "/api/v1/covers/"+track.CoverID, "", deniedKey); denied.Code != 403 {
		t.Fatalf("unauthorized artwork visible: %d", denied.Code)
	}
}

func TestUploadStageFailedCommitCanRetry(t *testing.T) {
	api, root := testAPI(t)
	preview, err := api.store.stageAudioWithAssets("retry.mp3", bytes.NewReader(testID3AudioMetadata()), nil, uploadAssets{Cover: tinyPNG()})
	if err != nil {
		t.Fatal(err)
	}
	before := api.store.Revision()
	if _, err := api.store.db.Exec(`CREATE TRIGGER fail_stage BEFORE INSERT ON tracks BEGIN SELECT RAISE(FAIL, 'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err == nil {
		t.Fatal("commit unexpectedly succeeded")
	}
	if api.store.Revision() != before || len(api.store.data.CoverAssets) != 0 {
		t.Fatal("failed commit changed snapshot")
	}
	entries, err := os.ReadDir(filepath.Join(root, "uploads"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed commit left final files: %v", err)
	}
	if _, ok, err := api.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatalf("stage not retryable: %v", err)
	}
	if _, err := api.store.db.Exec(`DROP TRIGGER fail_stage`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := api.store.db.QueryRow("SELECT count(*) FROM upload_staging").Scan(&count); err != nil || count != 0 {
		t.Fatalf("stage row not removed atomically: %v", err)
	}
}

func TestFailedStageCommitPreservesExistingLyricsFile(t *testing.T) {
	api, root := testAPI(t)
	preview, err := api.store.stageAudioWithAssets("collision.mp3", bytes.NewReader(testID3AudioMetadata()), nil, uploadAssets{Lyrics: []byte("[00:01]New lyrics")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "uploads", preview.ID+".lrc")
	existing := []byte("[00:02]Existing lyrics owned by another file")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err == nil {
		t.Fatal("commit unexpectedly overwrote an existing lyrics file")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, existing) {
		t.Fatalf("failed commit removed or changed existing lyrics: data=%q error=%v", actual, err)
	}
	if _, err := os.Stat(filepath.Join(root, "uploads", preview.ID+".mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed commit retained its new audio file: %v", err)
	}
	if _, ok, err := api.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatalf("failed stage is not available for retry: %v", err)
	}
}

func TestStageCleanupRemovesOrphansAndKeepsPendingUploads(t *testing.T) {
	api, root := testAPI(t)
	preview, err := api.store.StageAudio("pending.mp3", bytes.NewReader(testID3AudioMetadata()), nil)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, ".staging", "11111111111111111111111111111111.mp3")
	unrelated := filepath.Join(root, ".staging", "user-owned.mp3")
	for _, path := range []string{orphan, unrelated} {
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := api.store.CleanupUploadStages(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned upload file remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("cleanup removed a file outside its naming convention: %v", err)
	}
	if _, ok, err := api.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatalf("cleanup removed a pending upload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".staging", preview.ID+".mp3")); err != nil {
		t.Fatalf("cleanup removed a pending audio file: %v", err)
	}
}

func TestExpiredUploadCleanupDoesNotRequirePreviewRequest(t *testing.T) {
	api, root := testAPI(t)
	preview, err := api.store.StageAudio("expired.mp3", bytes.NewReader(testID3AudioMetadata()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("UPDATE upload_staging SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?", preview.ID); err != nil {
		t.Fatal(err)
	}
	if err := api.store.CleanupUploadStages(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired staging files remain: %v", err)
	}
}

func TestStageHTTPAcceptsLargeAudioWithResourceFiles(t *testing.T) {
	api, _ := testAPI(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "http.mp3")
	_, _ = part.Write(append(testID3AudioMetadata(), make([]byte, 3<<20)...))
	part, _ = form.CreateFormFile("lyrics_file", "http.lrc")
	_, _ = part.Write([]byte("[00:01]HTTP external"))
	part, _ = form.CreateFormFile("cover", "http.png")
	_, _ = part.Write(tinyPNG())
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/stage", &body)
	request.Header.Set("X-API-Key", "secret")
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("stage failed: %d %s", response.Code, response.Body)
	}
	var preview UploadPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Track.LyricsSource != "sidecar" || preview.Track.CoverSource != "manual" {
		t.Fatalf("sidecar preview response: %+v", preview)
	}
	commit := callAPI(api, "POST", "/api/v1/uploads/stage/"+preview.ID+"/commit", `{"title":"HTTP edit"}`)
	if commit.Code != 200 || !strings.Contains(commit.Body.String(), "HTTP edit") {
		t.Fatalf("commit response: %d %s", commit.Code, commit.Body)
	}
}

func stageCoverHTTP(t *testing.T, api *API, audio []byte, source string, image []byte) (UploadPreview, *httptest.ResponseRecorder) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "artwork.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(audio)
	part, err = form.CreateFormFile("cover", "artwork.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(image)
	_ = form.WriteField("cover_source", source)
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/stage", &body)
	request.Header.Set("X-API-Key", "secret")
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	var preview UploadPreview
	if response.Code == http.StatusCreated {
		if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
	}
	return preview, response
}

func TestStageAutomaticCoverPreservesEmbeddedArtwork(t *testing.T) {
	picture := append([]byte("\x00image/png\x00\x03\x00"), tinyPNG()...)
	audio := testID3Tag(testID3Frame("APIC", picture))
	for _, source := range []string{"external", "manual", ""} {
		t.Run(source, func(t *testing.T) {
			api, _ := testAPI(t)
			preview, response := stageCoverHTTP(t, api, audio, source, tinyJPEG())
			if response.Code != http.StatusCreated {
				t.Fatalf("stage status=%d response=%s", response.Code, response.Body)
			}
			wantImage, wantSource := tinyJPEG(), "manual"
			if source == "external" {
				wantImage, wantSource = tinyPNG(), "embedded"
			}
			asset, err := api.store.UploadStageCover(preview.ID)
			if err != nil || !bytes.Equal(asset.Data, wantImage) || preview.Track.CoverSource != wantSource {
				t.Fatalf("cover precedence incorrect: source=%q want=%q err=%v", preview.Track.CoverSource, wantSource, err)
			}
			track, _, err := api.store.CommitUploadStage(preview.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := api.store.Scan(); err != nil {
				t.Fatal(err)
			}
			stored, _ := api.store.Track(track.ID)
			asset, exists := api.store.Cover(stored.CoverID)
			if !exists || !bytes.Equal(asset.Data, wantImage) || stored.CoverSource != wantSource {
				t.Fatalf("rescan changed artwork precedence: source=%q want=%q", stored.CoverSource, wantSource)
			}
		})
	}
	api, _ := testAPI(t)
	if _, response := stageCoverHTTP(t, api, audio, "embedded", tinyJPEG()); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown cover_source accepted: %d %s", response.Code, response.Body)
	}
}

func TestUploadedExternalArtworkPersistsAsSidecar(t *testing.T) {
	api, root := testAPI(t)
	preview, response := stageCoverHTTP(t, api, testID3AudioMetadata(), "external", tinyJPEG())
	if response.Code != http.StatusCreated {
		t.Fatalf("stage status=%d response=%s", response.Code, response.Body)
	}
	track, _, err := api.store.CommitUploadStage(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(track.Path, filepath.Ext(track.Path))+".jpg"))
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, tinyJPEG()) {
		t.Fatalf("external image sidecar missing: %v", err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	rescanned, _ := api.store.Track(track.ID)
	if rescanned.CoverID != track.CoverID || rescanned.CoverSource != "external" || api.store.data.Overrides[track.ID]["cover_id"] != "" {
		t.Fatalf("external artwork precedence was frozen as manual: %+v", rescanned)
	}
	databasePath := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(databasePath, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Scan(); err != nil {
		t.Fatal(err)
	}
	stored, _ := reopened.Track(track.ID)
	if stored.CoverID != track.CoverID || stored.CoverSource != "external" {
		t.Fatalf("external artwork lost after restart: %+v", stored)
	}
}

func TestFailedExternalCoverCommitPreservesExistingImage(t *testing.T) {
	api, root := testAPI(t)
	preview, response := stageCoverHTTP(t, api, testID3AudioMetadata(), "external", tinyJPEG())
	if response.Code != http.StatusCreated {
		t.Fatalf("stage status=%d response=%s", response.Code, response.Body)
	}
	if err := os.MkdirAll(filepath.Join(root, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(root, "uploads", preview.ID+".jpg")
	existing := []byte("existing image file")
	if err := os.WriteFile(imagePath, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err == nil {
		t.Fatal("commit did not reject the pre-existing image path")
	}
	actual, err := os.ReadFile(imagePath)
	if err != nil || !bytes.Equal(actual, existing) {
		t.Fatalf("existing image changed on failed commit: %q %v", actual, err)
	}
	if _, err := os.Stat(filepath.Join(root, "uploads", preview.ID+".mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed image commit retained its audio: %v", err)
	}
	if _, ok, err := api.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatalf("failed image stage is not retryable: %v", err)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("CREATE TRIGGER fail_external_image BEFORE INSERT ON tracks BEGIN SELECT RAISE(ABORT, 'external image rollback'); END"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err == nil {
		t.Fatal("external image transaction unexpectedly succeeded")
	}
	if _, err := os.Stat(imagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed transaction retained its newly created image: %v", err)
	}
	if _, err := api.store.db.Exec("DROP TRIGGER fail_external_image"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err != nil {
		t.Fatalf("external image stage cannot retry: %v", err)
	}
}
