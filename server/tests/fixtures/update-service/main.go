// Isolated systemd fixture. Never packaged or deployed.
package main

import (
 "database/sql"
 "encoding/json"
 "log"
 "net/http"
 "os"
 "path/filepath"
 _ "modernc.org/sqlite"
)
func main(){
 binary,_:=os.Executable();data,err:=os.ReadFile(filepath.Join(filepath.Dir(binary),"build.json"));if err!=nil{log.Fatal(err)}
 var build map[string]any;if err=json.Unmarshal(data,&build);err!=nil{log.Fatal(err)}
 db,err:=sql.Open("sqlite",os.Getenv("TEST_DATABASE"));if err!=nil{log.Fatal(err)};defer db.Close()
 if build["version"]=="10.0.0"{db.Exec("UPDATE marker SET value='startup changed database'");build["commit"]="incorrect commit"}
 build["status"]="ok"
 http.HandleFunc("/api/v2/health",func(w http.ResponseWriter,r *http.Request){json.NewEncoder(w).Encode(build)})
 log.Fatal(http.ListenAndServe(os.Getenv("TEST_ADDRESS"),nil))
}
