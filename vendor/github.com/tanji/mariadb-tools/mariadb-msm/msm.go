// msm.go
package main

import (
	"bytes"
	_ "database/sql"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/mariadb-tools/dbhelper"
	"log"
	"net/smtp"
	"strings"
	"time"
)

var (
	version  = flag.Bool("version", false, "Return version")
	user     = flag.String("user", "", "User for MariaDB login, specified in the [user]:[password] format")
	host     = flag.String("host", "", "MariaDB host IP and port (optional), specified in the host:[port] format")
	socket   = flag.String("socket", "/var/run/mysqld/mysqld.sock", "Path of MariaDB unix socket")
	verbose  = flag.Bool("verbose", false, "Print detailed execution info")
	email    = flag.String("email", "", "Destination email address for alerts")
	interval = flag.Uint64("interval", 0, "Optional monitoring interval")
	from     = flag.String("from", "MariaDB Multisource Monitor <remotedba@mariadb.com>", "Sender name and email")
)

var failcount uint
var msg string
var recovery bool

const msmVersion string = "0.1.3"

func main() {
	flag.Parse()
	if *version == true {
		fmt.Println("MariaDB Multi Source Monitor version", msmVersion)
	}
	var address string
	if *socket != "" {
		address = "unix(" + *socket + ")"
	}
	if *host != "" {
		address = "tcp(" + *host + ")"
	}
	if *user == "" {
		log.Fatal("ERROR: No user/pair specified.")
	}
	dbUser, dbPass := splitPair(*user)
	db, err := sqlx.Connect("mysql", dbUser+":"+dbPass+"@"+address+"/")
	if err != nil {
		log.Fatal(err)
	}
	var hostname string
	db.Get(&hostname, "SELECT @@hostname")
	recovery = true
	header := "From: " + *from + "\nTo: <" + *email + ">\nSubject: " + hostname + " Replication Alert\n"
	for {
		status, err := dbhelper.GetAllSlavesStatus(db)
		if err != nil {
			log.Println(err)
		}
		if len(status) == 0 {
			log.Fatal("ERROR: Multisource replication is not configured on this server.")
		}
		failcount = 0
		msg = ""
		for _, v := range status {
			if v.Seconds_Behind_Master.Valid == true {
				sbm := v.Seconds_Behind_Master.Int64
				if *verbose {
					log.Printf("Connection name: %s Seconds behind master: %d\n", v.Connection_name, sbm)
				}
			} else {
				failcount++
				msg += fmt.Sprintf("Connection name: %s Status: Replication is stopped\n", v.Connection_name)
				msg += fmt.Sprintf("Last Error: %s\n", v.Last_Error)
			}
		}
		if *verbose && failcount > 0 {
			log.Print(msg)
		}
		if *email != "" && failcount > 0 && recovery == true {
			msg += "You are receiving this email because a multi-source replication channel has stopped on the following server: " + hostname + ".\nPlease take corrective actions as required.\n"
			mail(header + msg)
			recovery = false
		}
		if failcount == 0 {
			recovery = true
		}
		if *interval > 0 {
			time.Sleep(time.Duration(*interval) * time.Minute)
		} else {
			break
		}
	}
}

func mail(msg string) {
	// Connect to the remote SMTP server.
	c, err := smtp.Dial("localhost:25")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	// Set the sender and recipient.
	c.Mail(*from)
	c.Rcpt(*email)
	// Send the email body.
	wc, err := c.Data()
	if err != nil {
		log.Fatal(err)
	}
	defer wc.Close()
	buf := bytes.NewBufferString(msg)
	if _, err = buf.WriteTo(wc); err != nil {
		log.Fatal(err)
	}
}

/* Returns generic items from a pair, e.g. user:pass */
func splitPair(s string) (string, string) {
	items := strings.Split(s, ":")
	if len(items) == 1 {
		return items[0], ""
	} else {
		return items[0], items[1]
	}
}
