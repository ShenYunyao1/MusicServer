package main

import (
	"fmt"
	"testing"
)

func TestFormalNotesRequireMatchingVersionSchemaAndNoDraftTemplate(t *testing.T) {
	for _, notes := range []string{"MusicServer 下一版开发说明 — schema 9（尚未正式发行）", "# MusicServer v0.21.14\n schema 9", "# MusicServer v0.21.150\n schema 9", "# MusicServer v0.21.15\n schema 90", "# MusicServer v0.21.15\n schema 8", "# MusicServer v0.21.15\n schema 9 尚未正式发行"} {
		if err := validateReleaseNotes([]byte(notes), "0.21.15", 9); err == nil {
			t.Fatal("invalid notes passed", notes)
		}
	}
	for _, schemaText := range []string{"schema 9", "schema：9", "schema为9"} {
		notes := fmt.Sprintf("# MusicServer v0.21.15\n<!-- musicserver-release: {\"version\":\"0.21.15\",\"schema\":9,\"status\":\"stable\"} -->\n数据库 %s；保留现有曲库。", schemaText)
		if err := validateReleaseNotes([]byte(notes), "0.21.15", 9); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSigningAlwaysRequiresFormalValidation(t *testing.T) {
	for _, flags := range []struct{ sign, formal, expected bool }{{true, false, true}, {true, true, true}, {false, true, true}, {false, false, false}} {
		if formalPackagingRequired(flags.sign, flags.formal) != flags.expected {
			t.Fatal(flags)
		}
	}
}

func TestFormalNotesRejectLooseHeadingAndAdditionalDraftMarkers(t *testing.T) {
	for _, notes := range []string{
		"# Notes for v0.21.15 and other versions\n schema 9",
		"# MusicServer v0.21.15\n schema 9 草稿",
		"# MusicServer v0.21.15\n schema 9 UNRELEASED",
	} {
		if err := validateReleaseNotes([]byte(notes), "0.21.15", 9); err == nil {
			t.Fatal("informal notes accepted", notes)
		}
	}
}

func TestFormalNotesRejectExplicitPreviewStatusButAllowFeaturePreview(t *testing.T) {
	base := "# MusicServer v0.21.15\n<!-- musicserver-release: {\"version\":\"0.21.15\",\"schema\":9,\"status\":\"stable\"} -->\nschema 9\n"
	for _, status := range []string{"发行状态：预览版", "**发布状态：预发布**", "## 预发布版本", "当前版本为预览版", "Release status: pre-release", "This release is a prerelease"} {
		if err := validateReleaseNotes([]byte(base+status), "0.21.15", 9); err == nil {
			t.Errorf("explicit preview declaration accepted: %s", status)
		}
	}
	if err := validateReleaseNotes([]byte(base+"支持歌词预览、封面预览与正式版本的预发布检查功能。"), "0.21.15", 9); err != nil {
		t.Fatal("feature prose mistaken for release status", err)
	}
}

func TestFormalNotesRejectAmbiguousMetadata(t *testing.T) {
	for _, record := range []string{
		`{"version":"0.21.15","schema":9,"status":"preview"}`,
		`{"version":"0.21.15","schema":9,"status":"preview","status":"stable"}`,
		`{"version":"0.21.15","schema":9,"schema":9,"status":"stable"}`,
	} {
		if err := validateReleaseNotes([]byte("# MusicServer v0.21.15\n<!-- musicserver-release: "+record+" -->\nschema 9"), "0.21.15", 9); err == nil {
			t.Fatal("ambiguous/nonstable metadata accepted", record)
		}
	}
}
