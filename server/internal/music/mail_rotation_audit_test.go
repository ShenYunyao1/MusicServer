package music

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMailRotationRetiresOnlyCommittedOldCredential(t *testing.T) {
	a, _ := testAPI(t)
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	input := MailSettingsInput{Host: "smtp.example.com", Port: 465, TLSMode: "tls", FromAddress: "sender@example.com", Username: "sender", Password: "old-credential"}
	first, err := a.store.UpdateMailSettings(input, MutationOptions{UserID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	old, err := readMailSettings(a.store.db)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := a.store.mailCredential(old)
	if err != nil {
		t.Fatal(err)
	}
	input.Version = first.Version
	input.Password = "new-credential"
	if _, err := a.store.UpdateMailSettings(input, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.store.mailSecretsDirectory(), old.SecretID+".json")); !os.IsNotExist(err) {
		t.Fatal("old credential persisted", err)
	}
	if credential.Password != "old-credential" {
		t.Fatal("an already loaded in-flight credential changed")
	}
	current, err := readMailSettings(a.store.db)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := a.store.mailCredential(current)
	if err != nil || loaded.Password != "new-credential" {
		t.Fatal(loaded, err)
	}
}
