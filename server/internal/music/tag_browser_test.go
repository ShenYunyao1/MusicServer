package music

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This isolated fixture uses the normal current-schema application model.
func TestTagEditorLiveBrowser(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_TAG_UI") != "1" {
		t.Skip("live browser acceptance was not requested")
	}
	a, _ := tagHTTPAPI(t)
	auth := tagTestAuth(t, a.store, "admin")
	_ = mustLinkTag(t, a.store, auth, "public", "Rock")
	_ = mustLinkTag(t, a.store, auth, "personal", "My music")
	server := httptest.NewServer(a)
	defer server.Close()
	script, err := filepath.Abs("../../tests/tag-editor-live-ui.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "MUSIC_SERVER_TAG_LIVE_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live tag browser: %v\n%s", err, output)
	}
	t.Log(string(output))
	view, err := a.store.GetTrackTags(auth, a.store.Tracks()[0].ID)
	if err != nil || len(view.Personal) != 1 || view.Personal[0].Name != "My music" {
		t.Fatal("browser changes were not persisted", view, err)
	}
}
