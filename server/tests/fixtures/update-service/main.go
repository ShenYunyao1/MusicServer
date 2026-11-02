// Isolated systemd fixture. Never packaged or deployed.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	if build["schema"] == float64(8) || build["schema"] == float64(9) {
		target := int(build["schema"].(float64))
		// This isolated service emulates a new app's transactional migration.
		// The real application's full-data migration is tested in music.
		var schema int
		if err = db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
			log.Fatal(err)
		}
		if schema+1 == target {
			tx, err := db.Begin()
			if err != nil {
				log.Fatal(err)
			}
			if _, err = tx.Exec(fmt.Sprintf("CREATE TABLE migration_fixture(id INTEGER PRIMARY KEY); UPDATE meta SET value='%d' WHERE key='schema_version'; PRAGMA user_version=%d;", target, target)); err != nil {
				tx.Rollback()
				log.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				log.Fatal(err)
			}
		} else if schema != target {
			log.Fatal("unsupported fixture schema")
		}
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
