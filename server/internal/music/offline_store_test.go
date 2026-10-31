package music

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineAccountStorePreservesUploadCheckpoints(t *testing.T) {
	a, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, a.store, "admin")
	task := resumableCheckpoint{resumableUpload: resumableUpload{ID: "01234567890123456789012345678901", Name: "pending.wav", Size: 44, State: "uploading", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, Owner: user.ID}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec("INSERT INTO meta(key,value) VALUES(?,?)", resumableMetaPrefix+task.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	offline, err := NewOfflineStore(a.store.DataPath(), filepath.Join(t.TempDir(), "wrong-media-root"))
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close()
	if err := offline.ResetPassword(user.Username, "offline reset preserves uploads", false); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := offline.db.QueryRow("SELECT value FROM meta WHERE key=?", resumableMetaPrefix+task.ID).Scan(&after); err != nil || after != string(raw) {
		t.Fatal("offline operation changed resume checkpoint", after, err)
	}
}
