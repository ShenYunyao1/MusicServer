package music

import (
	"strings"
	"testing"
)

func TestPikmeloBranding(t *testing.T) {
	page := string(managementPage)
	for _, name := range []string{"拾律服务器", "Pikmelo Server"} {
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
