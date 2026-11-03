package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

func validateReleaseNotes(data []byte, release string, schema int) error {
	notes := strings.TrimSpace(string(data))
	heading, _, _ := strings.Cut(notes, "\n")
	if strings.TrimSpace(heading) != "# MusicServer v"+release {
		return fmt.Errorf("formal release notes heading must be # MusicServer v%s", release)
	}
	if regexp.MustCompile(`(?i)\b(unreleased|draft|development)\b|草稿|尚未正式发行|下一版开发说明|待发布`).MatchString(notes) {
		return fmt.Errorf("formal release notes contain development placeholders")
	}
	// Metadata is the authoritative machine-readable release status. This
	// auxiliary check rejects explicit contradictory declarations, not feature
	// descriptions such as "歌词预览". Arbitrary prose still needs human review.
	preview := regexp.MustCompile(`(?i)^(?:(?:预览|预发布)(?:版本|版)?(?:$|[ \t。，；;：:])|(?:preview(?: release)?|pre-?release)\b)`)
	declaration := regexp.MustCompile(`(?i)^(?:(?:发行|发布|版本)状态\s*[:：]\s*|(?:当前|此|本)(?:版本|发行)(?:为|是)\s*|release\s+status\s*:\s*|this\s+release\s+is\s+(?:a\s+)?)`)
	for _, line := range strings.Split(notes, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "#*- \t\r")
		status := declaration.ReplaceAllString(line, "")
		if preview.MatchString(status) {
			return fmt.Errorf("formal release notes declare preview/prerelease status")
		}
	}
	metadata := regexp.MustCompile(`(?m)^<!-- musicserver-release: (\{[^\r\n]*\}) -->\r?$`).FindAllSubmatch(data, -1)
	if len(metadata) != 1 {
		return fmt.Errorf("formal release notes require exactly one musicserver-release metadata comment")
	}
	// encoding/json otherwise silently accepts a repeated status/version key.
	keys := json.NewDecoder(bytes.NewReader(metadata[0][1]))
	if _, err := keys.Token(); err != nil {
		return fmt.Errorf("invalid release metadata: %w", err)
	}
	seen := map[string]bool{}
	for keys.More() {
		token, err := keys.Token()
		if err != nil {
			return fmt.Errorf("invalid release metadata: %w", err)
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("release metadata fields must be unique")
		}
		seen[key] = true
		if err := keys.Decode(new(json.RawMessage)); err != nil {
			return fmt.Errorf("invalid release metadata: %w", err)
		}
	}
	var record struct {
		Version string `json:"version"`
		Schema  int    `json:"schema"`
		Status  string `json:"status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(metadata[0][1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return fmt.Errorf("invalid release metadata: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF || record.Version != release || record.Schema != schema || record.Status != "stable" {
		return fmt.Errorf("release metadata must match version, schema and stable status")
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)\bschema(?:\s+|\s*[:：为]\s*)%d\b`, schema)).MatchString(notes) {
		return fmt.Errorf("formal release notes must identify schema %d", schema)
	}
	return nil
}

func formalPackagingRequired(sign, formal bool) bool { return sign || formal }
