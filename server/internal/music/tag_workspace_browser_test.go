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

func TestTagWorkspaceLiveBrowser(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_TAG_WORKSPACE_UI") != "1" {
		t.Skip("live tag workspace acceptance was not requested")
	}
	a, _, _, _, _ := filterIntegrationFixture(t)
	readyMember(t, a, "workspace-member")
	server := httptest.NewServer(a)
	defer server.Close()
	script, err := filepath.Abs("../../tests/tag-workspace-live-ui.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "MUSIC_SERVER_TAG_WORKSPACE_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live tag workspace browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
