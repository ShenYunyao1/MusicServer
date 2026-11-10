package version

import (
	"strings"
	"testing"
)

func TestContainerSourceMetadata(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, tc := range []struct {
		commit, modified, want string
		dirty                  bool
	}{
		{hash, "false", hash, false}, {hash, "true", hash, true},
		{"unknown", "false", "unknown", true}, {strings.Repeat("A", 40), "false", "unknown", true},
		{strings.Repeat("g", 40), "false", "unknown", true}, {hash, "", "unknown", true},
	} {
		commit, dirty := containerSource(tc.commit, tc.modified)
		if commit != tc.want || dirty != tc.dirty {
			t.Fatalf("source(%q,%q)=(%q,%t)", tc.commit, tc.modified, commit, dirty)
		}
	}
}
