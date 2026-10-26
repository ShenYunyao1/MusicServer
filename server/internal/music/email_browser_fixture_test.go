package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This harness exists only in the Go test binary. It uses the real HTTP and
// persistence paths, replacing only SMTP delivery with private fixture files.
func TestEmailBrowserFixture(t *testing.T) {
	directory := os.Getenv("MUSIC_SERVER_EMAIL_BROWSER_FIXTURE_DIR")
	if directory == "" {
		t.Skip("isolated browser harness is not requested")
	}
	if !filepath.IsAbs(directory) {
		t.Fatal("browser fixture directory must be absolute")
	}
	data := filepath.Join(directory, "library.sqlite")
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("browser fixture requires a new isolated database")
	}
	media := filepath.Join(directory, "media")
	if err := os.MkdirAll(filepath.Join(directory, "deliveries"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(data, media)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.mail.mu.Lock()
	store.mail.sendHook = func(_ context.Context, _ MailSettings, _ mailSecret, envelope mailEnvelope, id string) error {
		payload, err := json.Marshal(struct {
			mailEnvelope
			ID string `json:"id"`
		}{envelope, id})
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(directory, "deliveries", id+".json"), payload, 0600)
	}
	store.mail.mu.Unlock()
	if _, err := store.InitializeAdmin("admin", "email fixture administrator password"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store)
	if err := api.ConfigureWebSetup(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	metadata, _ := json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(filepath.Join(directory, "server.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(170 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(directory, "stop")); err == nil {
				return
			}
			// Deterministically cross the resend cooldown in this isolated test DB.
			// This file control is compiled out of the production server.
			control := filepath.Join(directory, "age-challenge.json")
			if raw, err := os.ReadFile(control); err == nil {
				var input struct {
					ID string `json:"id"`
				}
				if len(raw) > 1024 || json.Unmarshal(raw, &input) != nil || len(input.ID) != 36 || !validID.MatchString(input.ID) {
					t.Fatal("invalid browser fixture clock control")
				}
				if _, err := readUser(store.db, input.ID); err != nil {
					t.Fatal(err)
				}
				if err := store.transaction(func(tx *sql.Tx) error {
					result, err := tx.Exec("UPDATE meta SET value=json_set(value,'$.created_at',?) WHERE key=?", persistedTime(time.Now().Add(-61*time.Second)), "email_challenge/"+input.ID)
					if err != nil {
						return err
					}
					count, err := result.RowsAffected()
					if err != nil {
						return err
					}
					if count != 1 {
						return os.ErrNotExist
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(control); err != nil {
					t.Fatal(err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		case <-deadline.C:
			t.Fatal("email browser harness timed out")
		}
	}
}
