package music

import (
	"fmt"
	"strings"
)

// Validate relational existence and removal state, not full song metadata (which
// may contain megabytes of lyrics). Chunk parameters and retain input ordering
// for errors. Previously retained removed members remain valid.
func validatePlaylistMembers(reader sqlReader, before, after []string) error {
	old := map[string]bool{}
	for _, id := range before {
		old[id] = true
	}
	states := map[string]bool{}
	for offset := 0; offset < len(after); offset += 500 {
		end := min(len(after), offset+500)
		args := make([]any, end-offset)
		for i, id := range after[offset:end] {
			args[i] = id
		}
		rows, err := reader.Query("SELECT id,removed FROM tracks WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+")", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var removed bool
			if err = rows.Scan(&id, &removed); err != nil {
				rows.Close()
				return err
			}
			states[id] = removed
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for _, id := range after {
		removed, exists := states[id]
		if !exists || removed && !old[id] {
			return fmt.Errorf("unknown track: %s", id)
		}
	}
	return nil
}
