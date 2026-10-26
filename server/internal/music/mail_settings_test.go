package music

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMailAddressNormalization(t *testing.T) {
	for input, want := range map[string]string{"Person+tag@EXAMPLE.COM": "Person+tag@example.com", " person@example.com ": "person@example.com"} {
		got, err := normalizeMailAddress(input)
		if err != nil || got != want {
			t.Fatalf("%q => %q %v", input, got, err)
		}
	}
	for _, input := range []string{"Display <a@example.com>", "a@example.com\r\nBcc:other@example.com", "a@example.com\r\n", "\na@example.com", "a@example.com,other@example.com", "a@localhost", "a..b@example.com", "a@-example.com"} {
		if _, err := normalizeMailAddress(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestMailSettingsRequireTestAndNeverExposePassword(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	settings, err := api.store.UpdateMailSettings(MailSettingsInput{Host: "smtp.example.com", Port: 465, TLSMode: "tls", FromAddress: "sender@example.com", Username: "sender", Password: "private-smtp-password"}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Configured || settings.Tested || api.store.MailReady() {
		t.Fatal(settings)
	}
	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), "private-smtp-password") {
		t.Fatal("password exposed")
	}
	if _, err = api.store.UpdateMailSettings(MailSettingsInput{Host: "smtp.example.com", Port: 465, TLSMode: "tls", FromAddress: "sender@example.com", Username: "sender", Version: 0}, MutationOptions{UserID: user.ID}); err == nil {
		t.Fatal("stale write accepted")
	}
}

func TestMailSettingsFailedCommitKeepsPriorCredentials(t *testing.T) {
	api, auth, _ := mailTestSetup(t)
	s := api.store
	before, err := os.ReadDir(s.mailSecretsDirectory())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("CREATE TRIGGER reject_mail_change BEFORE UPDATE ON settings WHEN OLD.key='mail_settings' BEGIN SELECT RAISE(ABORT,'isolated mail commit failure'); END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateMailSettings(MailSettingsInput{Host: "smtp.other.example.com", Port: 465, TLSMode: "tls", FromAddress: "sender@example.com", Username: "sender", Password: "replacement-secret-not-committed", Version: 1}, MutationOptions{UserID: auth.userID})
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	after, err := os.ReadDir(s.mailSecretsDirectory())
	if err != nil || len(after) != len(before) {
		t.Fatal("uncommitted credential file leaked", len(before), len(after), err)
	}
	for i := range before {
		if before[i].Name() != after[i].Name() {
			t.Fatal("previous credentials replaced")
		}
	}
	settings, err := s.MailSettings()
	if err != nil || settings.Version != 1 || !settings.Tested || !s.MailReady() {
		t.Fatal("previous mail configuration damaged", settings, err)
	}
}
