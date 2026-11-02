package music

import "strings"

func scanPathUnreadable(path string, unreadable map[string]bool) bool {
	for {
		if unreadable[path] {
			return true
		}
		end := strings.LastIndex(path, "/")
		if end < 0 {
			return false
		}
		path = path[:end]
	}
}
