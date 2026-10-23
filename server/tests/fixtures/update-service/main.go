// Isolated systemd fixture. Never packaged or deployed.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	_ "modernc.org/sqlite"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	binary, _ := os.Executable()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binary), "build.json"))
	if err != nil {
		log.Fatal(err)
	}
	var build map[string]any
	if err = json.Unmarshal(data, &build); err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("sqlite", os.Getenv("TEST_DATABASE"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		log.Fatal(err)
	}
	if build["version"] == "10.0.0" {
		if _, err = db.Exec("UPDATE marker SET value='startup changed database'"); err != nil {
			log.Fatal(err)
		}
		build["commit"] = "incorrect commit"
	}
	build["status"] = "ok"
	http.HandleFunc("/api/v2/health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(build) })
	log.Fatal(http.ListenAndServe(os.Getenv("TEST_ADDRESS"), nil))
}
