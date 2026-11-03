package main

import "testing"

func TestFormalNotesRequireMatchingVersionSchemaAndNoDraftTemplate(t *testing.T) {
	for _, notes := range []string{"MusicServer 下一版开发说明 — schema 9（尚未正式发行）", "# MusicServer v0.21.14\n schema 9", "# MusicServer v0.21.150\n schema 9", "# MusicServer v0.21.15\n schema 90", "# MusicServer v0.21.15\n schema 8", "# MusicServer v0.21.15\n schema 9 尚未正式发行"} {
		if err := validateReleaseNotes([]byte(notes), "0.21.15", 9); err == nil {
			t.Fatal("invalid notes passed", notes)
		}
	}
	if err := validateReleaseNotes([]byte("# MusicServer v0.21.15\n\n数据库 schema 9；保留现有曲库。"), "0.21.15", 9); err != nil {
		t.Fatal(err)
	}
}
