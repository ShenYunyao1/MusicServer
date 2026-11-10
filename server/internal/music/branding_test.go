package music

import (
	"encoding/hex"
	"strings"
	"testing"

	"pikmelo-server/internal/version"
)

func TestPikmeloPreservesMailKeyDerivation(t *testing.T) {
	for purpose, expected := range map[string]string{
		"outbox":    "99bc6220d49013b5c3892aadd40a74f53d3c0b23aa0a229207dc91f32dc0f7b4",
		"challenge": "bb2d6ca5eb564d5ab1a08f8a94284f0bff8fe9d0887cba1e19dee6d1eca64f7d",
	} {
		if actual := hex.EncodeToString(mailDerivedKey([]byte("brand-compatibility-test-master"), purpose)); actual != expected {
			t.Fatalf("existing mail %s key changed: %s", purpose, actual)
		}
	}
}

func TestPikmeloBranding(t *testing.T) {
	page := string(managementPage)
	for _, name := range []string{version.ChineseName, version.ProductName} {
		if !strings.Contains(page, name) {
			t.Errorf("management page does not identify %s", name)
		}
	}
	for _, name := range []string{"account.js", "navigation.js", "library.js", "playlist-ui.js", "sharing.js", "community.js", "community-realtime.js", "setup.js"} {
		var data []byte
		var err error
		if name == "account.js" {
			data = accountPageScript
		} else {
			data, err = workspaceAssets.ReadFile(name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "音乐服务器") {
			t.Errorf("%s still displays the previous product name", name)
		}
	}
	for _, test := range []bool{false, true} {
		message := string(smtpMessage(MailSettings{FromAddress: "server@example.com"}, mailEnvelope{To: "member@example.com", Code: "123456", Test: test}, "test"))
		if !strings.Contains(message, "Subject: Pikmelo Server email ") {
			t.Errorf("mail subject does not identify Pikmelo Server")
		}
	}
}
