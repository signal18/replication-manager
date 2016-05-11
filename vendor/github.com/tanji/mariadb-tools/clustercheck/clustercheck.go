// clustercheck.go
package main

import (
	_ "database/sql"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/mariadb-tools/dbhelper"
	"io/ioutil"
	"log"
	"net/http"
	"os/user"
	"strings"
)

var (
	awd    = flag.Bool("a", false, "Available when donor")
	dwr    = flag.Bool("d", false, "Disable when read_only flag is set (desirable when wanting to take a node out of the cluster without desync)")
	cnf    = flag.String("c", "~/.my.cnf", "MySQL Config file to use")
	port   = flag.Int("p", 8000, "Port to listen on")
	myvars map[string]string
	db     *sqlx.DB
)

func main() {
	flag.Parse()
	usr, _ := user.Current()
	dir := usr.HomeDir
	conf := *cnf
	if strings.Contains(conf, "~/") {
		conf = strings.Replace(conf, "~", dir, 1)
	}
	myvars = confParser(conf)
	httpAddr := fmt.Sprintf(":%v", *port)
	log.Printf("Listening to %v", httpAddr)
	http.HandleFunc("/", clustercheck)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}

func clustercheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/html")
	db = dbhelper.Connect(myvars["user"], myvars["password"], dbhelper.GetAddress(myvars["host"], myvars["port"], myvars["socket"]))
	defer db.Close()
	var (
		readonly string
		state    int
	)
	if *dwr == true {
		db.QueryRow("select variable_value as readonly from information_schema.global_variables where variable_name='read_only'").Scan(&readonly)
	}
	err := db.QueryRow("select variable_value as state from information_schema.global_status where variable_name='wsrep_local_state'").Scan(&state)
	if err != nil {
		w.WriteHeader(503)
		fmt.Fprintf(w, "Cannot check cluster state: %v", err)
	} else if (*dwr == false && state == 4) || (*awd == false && state == 2) || (*dwr == true && readonly == "OFF" && state == 4) {
		fmt.Fprint(w, "MariaDB Cluster Node is synced.")
	} else {
		w.WriteHeader(503)
		fmt.Fprint(w, "MariaDB Cluster Node is not synced.")
	}
}

func confParser(configFile string) map[string]string {
	names := []string{"user", "password", "host", "port", "socket"}
	params := make(map[string]string)
	file, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Fatal(err)
	}
	lines := strings.Split(string(file), "\n")
	for _, line := range lines {
		for _, name := range names {
			if strings.Index(line, name) == 0 {
				res := strings.Split(line, "=")
				params[name] = res[1]
			}
		}
	}
	if params["user"] == "" {
		user, _ := user.Current()
		params["user"] = user.Username
	}
	return params
}
