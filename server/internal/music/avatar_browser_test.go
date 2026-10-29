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

func TestAvatarLiveBrowser(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_AVATAR_UI") != "1" {
		t.Skip("live avatar acceptance was not requested")
	}
	a, _ := testAPI(t)
	_, token := readyMember(t, a, "avatar-member")
	key := communityKey(t, a, token, "library")
	server := httptest.NewServer(a)
	defer server.Close()
	script, err := filepath.Abs("../../tests/avatar-live-ui.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "MUSIC_SERVER_AVATAR_LIVE_URL="+server.URL, "MUSIC_SERVER_AVATAR_TEST_KEY="+key)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live avatar browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
