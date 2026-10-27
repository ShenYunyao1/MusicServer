//go:build !linux

package update

import "errors"

func readWorkerCapabilities(string) (workerCapabilities, error) {
	return workerCapabilities{}, errors.New("automatic installation requires the independent Linux systemd updater")
}

func (i *Installer) WriteCapabilities() error {
	return errors.New("automatic installation requires the independent Linux systemd updater")
}
