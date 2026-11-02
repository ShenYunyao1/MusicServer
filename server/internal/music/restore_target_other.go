//go:build !linux && !windows

package music

import "errors"

func restoreDestinationMount(string) (bool, error) {
	return false, errors.New("restore filesystem boundary verification supports Windows and Linux")
}
