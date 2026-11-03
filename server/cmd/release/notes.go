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
	metadata := regexp.MustCompile(`(?m)^<!-- musicserver-release: (\{[^\r\n]*\}) -->\r?$`).FindAllSubmatch(data, -1)
	if len(metadata) != 1 {
		return fmt.Errorf("formal release notes require exactly one musicserver-release metadata comment")
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
