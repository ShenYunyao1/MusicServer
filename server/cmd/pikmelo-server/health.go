package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"pikmelo-server/internal/version"
)

func checkHealth(address string) error {
	client := &http.Client{
		Timeout:       3 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("health redirects are forbidden") },
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned HTTP %d", response.StatusCode)
	}
	var health struct {
		Status, Version string
		Schema          int
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return err
	}
	if health.Status != "ok" || health.Version != version.Version || health.Schema != version.Schema {
		return errors.New("health status or server identity mismatch")
	}
	return nil
}
