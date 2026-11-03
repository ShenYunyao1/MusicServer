package main

import (
	"fmt"
	"regexp"
	"strings"
)

func validateReleaseNotes(data []byte, release string, schema int) error {
	notes := strings.TrimSpace(string(data))
	heading, _, _ := strings.Cut(notes, "\n")
	if !regexp.MustCompile(`(^|[^A-Za-z0-9.])v` + regexp.QuoteMeta(release) + `([^A-Za-z0-9.]|$)`).MatchString(heading) {
		return fmt.Errorf("formal release notes heading must contain v%s", release)
	}
	for _, placeholder := range []string{"尚未正式发行", "下一版开发说明"} {
		if strings.Contains(notes, placeholder) {
			return fmt.Errorf("formal release notes contain development placeholder %q", placeholder)
		}
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)\bschema\s+%d\b`, schema)).MatchString(notes) {
		return fmt.Errorf("formal release notes must identify schema %d", schema)
	}
	return nil
}
