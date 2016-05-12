package main

import (
	_ "database/sql"
	"flag"
	"fmt"
	"github.com/dustin/go-humanize"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/mariadb-tools/common"
	"github.com/tanji/mariadb-tools/dbhelper"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"
)

var status map[string]int64
var prevStatus map[string]int64
var variable map[string]string

type timeSeries struct {
	Name    string
	Columns []string
	Points  [][]int64
}

var version = flag.Bool("version", false, "Return version")
var user = flag.String("user", "", "User for MariaDB login")
var password = flag.String("password", "", "Password for MariaDB login")
var host = flag.String("host", "", "MariaDB host IP address or FQDN")
var socket = flag.String("socket", "/var/run/mysqld/mysqld.sock", "Path of MariaDB unix socket")
var port = flag.String("port", "3306", "TCP Port of MariaDB server")

func main() {

	flag.Parse()
	if *version == true {
		fmt.Println("MariaDB Tools version 0.0.1")
		os.Exit(0)
	}
	var address string
	if *socket != "" {
		address = "unix(" + *socket + ")"
	}
	if *host != "" {
		address = "tcp(" + *host + ":" + *port + ")"
	}

	db, _ := sqlx.Open("mysql", *user+":"+*password+"@"+address+"/")
	err := db.Ping()
	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	status = dbhelper.GetStatusAsInt(db)
	variable, _ = dbhelper.GetVariables(db)

	out, err := exec.Command("uname", "-srm").Output()
	if err != nil {
		log.Fatal(err)
	}
	hostname, _ := os.Hostname()
	fmt.Printf("### MariaDB Server report for host %s\n", hostname)
	fmt.Printf("### %-25s%s", "Kernel version", out)
	fmt.Printf("### %-25s%s\n", "System Time", time.Now().Format("2006-01-02 at 03:04 (MST)"))
	fmt.Println(common.DrawHashline("General", 60))
	var server_version string
	db.QueryRow("SELECT VERSION()").Scan(&server_version)
	pPrintStr("Version", server_version)
	now := time.Now().Unix()
	uptime := status["UPTIME"]
	start_time := time.Unix(now-uptime, 0).Local()
	pPrintStr("Started", humanize.Time(start_time))
	var count int64
	db.Get(&count, "SELECT COUNT(*) FROM information_schema.schemata")
	pPrintInt("Databases", count)
	db.Get(&count, "SELECT COUNT(*) FROM information_schema.tables")
	pPrintInt("Tables", count) /* Potentially unsafe for large systems */
	pPrintStr("Datadir", variable["DATADIR"])
	pPrintStr("Binary Log", variable["LOG_BIN"])
	if variable["LOG_BIN"] == "ON" {
		pPrintStr("Binlog writes per hour", humanize.IBytes(uint64(status["BINLOG_BYTES_WRITTEN"]/status["UPTIME"])*3600))
	}
	// Add stuff for slow logs
	slaveStatus, err := dbhelper.GetSlaveStatus(db)
	if err != nil {
		slaveIO := slaveStatus.Slave_IO_Running
		slaveSQL := slaveStatus.Slave_SQL_Running
		var slaveState string
		if slaveIO == "Yes" && slaveSQL == "Yes" {
			slaveState = "Slave configured, threads running"
		} else {
			slaveState = "Slave configured, threads stopped"
		}
		pPrintStr("Replication", slaveState)
	} else {
		pPrintStr("Replication", "Not configured")
	}

	// InnoDB
	fmt.Println(common.DrawHashline("InnoDB", 60))
	ibps := humanize.IBytes(common.StrtoUint(variable["INNODB_BUFFER_POOL_SIZE"]))
	pPrintStr("InnoDB Buffer Pool", ibps)
	ibpsPages := float64(status["INNODB_BUFFER_POOL_PAGES_TOTAL"])
	ibpsFree := float64(status["INNODB_BUFFER_POOL_PAGES_FREE"])
	ibpsUsed := common.DecimaltoPctLow(ibpsFree, ibpsPages)
	pPrintStr("InnoDB Buffer Used", strconv.Itoa(ibpsUsed)+"%")
	ibpsDirty := float64(status["INNODB_BUFFER_POOL_PAGES_DIRTY"])
	ibpsDirtyPct := common.DecimaltoPct(ibpsDirty, ibpsPages)
	pPrintStr("InnoDB Buffer Dirty", strconv.Itoa(ibpsDirtyPct)+"%")
	pPrintStr("InnoDB Log Files", string(variable["INNODB_LOG_FILES_IN_GROUP"])+" files of "+humanize.IBytes(common.StrtoUint(variable["INNODB_LOG_FILE_SIZE"])))
	pPrintStr("InnoDB log writes per hour", humanize.IBytes(uint64(status["INNODB_OS_LOG_WRITTEN"]/status["UPTIME"])*3600))
	pPrintStr("InnoDB Log Buffer", humanize.IBytes(common.StrtoUint(variable["INNODB_LOG_BUFFER_SIZE"])))
	var iftc string
	switch variable["INNODB_FLUSH_LOG_AT_TRX_COMMIT"] {
	case "0":
		iftc = "0 - Flush log and write buffer every sec"
	case "1":
		iftc = "1 - Write buffer and Flush log at each trx commit"
	case "2":
		iftc = "2 - Write buffer at each trx commit, Flush log every sec"
	}
	pPrintStr("InnoDB Flush Log", iftc)
	ifm := variable["INNODB_FLUSH_METHOD"]
	if ifm == "" {
		ifm = "fsync"
	}
	pPrintStr("InnoDB Flush Method", ifm)
	pPrintStr("InnoDB IO Capacity", variable["INNODB_IO_CAPACITY"])
	// MyISAM
	fmt.Println(common.DrawHashline("MyISAM", 60))
	kbs := humanize.IBytes(common.StrtoUint(variable["KEY_BUFFER_SIZE"]))
	pPrintStr("MyISAM Key Cache", kbs)
	kbs_free := float64(status["KEY_BLOCKS_UNUSED"])
	kbs_used := float64(status["KEY_BLOCKS_USED"])
	kbsUsedPct := int(((1 - (kbs_free / (kbs_free + kbs_used))) * 100) + 0.5)
	pPrintStr("MyISAM Cache Used", strconv.Itoa(kbsUsedPct)+"%")
	// Handlers
	pPrintInt("Open tables", status["OPEN_TABLES"])
	pPrintInt("Open files", status["OPEN_FILES"])
}

func pPrintStr(name string, value string) {
	fmt.Printf("    %-25s%-20s\n", name, value)
}

func pPrintInt(name string, value int64) {
	fmt.Printf("    %-25s%-20d\n", name, value)
}
