package music

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRound2UnknownKeySuffixCannotDeleteKey(t *testing.T) {
	a, _ := testAPI(t)
	_, _, token := loginTestAccount(t, a.store, "admin")
	created := sessionRequest(a, token, "POST", "/api/v2/auth/keys", `{"name":"Protected key","scopes":["library:read"],"allowed_playlist_ids":["library"]}`)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	keys := a.store.APIKeys()
	key := keys[len(keys)-1]
	for _, suffix := range []string{"/test", "/unknown", "/rotate/extra"} {
		w := sessionRequest(a, token, "DELETE", "/api/v2/auth/keys/"+key.ID+suffix, "")
		if w.Code != 404 {
			t.Fatalf("unknown route %s returned %d %s", suffix, w.Code, w.Body)
		}
		found := false
		for _, k := range a.store.APIKeys() {
			found = found || k.ID == key.ID
		}
		if !found {
			t.Fatal("unknown route deleted key", suffix)
		}
	}
}

func TestRound2RestoreCleanupRequiresOwnershipMarker(t *testing.T) {
	parent := t.TempDir()
	name := ".musicserver-restore-11111111111111111111111111111111-123"
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("not owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CleanupRestoreStaging(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Fatal("unmarked directory was removed", err)
	}
}
