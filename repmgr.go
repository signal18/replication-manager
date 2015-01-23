package main

import (
	"database/sql"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/mariadb-tools/common"
	"github.com/mariadb-tools/dbhelper"
	"github.com/nsf/termbox-go"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	status     map[string]int64
	prevStatus map[string]int64
	variable   map[string]string
	slaveList  []string
	exit       bool
)

var (
	master    *sqlx.DB
	version   = flag.Bool("version", false, "Return version")
	user      = flag.String("user", "", "User for MariaDB login, specified in the [user]:[password] format")
	masterUrl = flag.String("host", "", "MariaDB master host IP and port (optional), specified in the host:[port] format")
	socket    = flag.String("socket", "/var/run/mysqld/mysqld.sock", "Path of MariaDB unix socket")
	rpluser   = flag.String("rpluser", "", "Replication user in the [user]:[password] format")
	// command specific-options
	slaves      = flag.String("slaves", "", "List of slaves connected to MariaDB master, separated by a comma")
	interactive = flag.Bool("interactive", true, "Runs the MariaDB monitor in interactive mode")
	verbose     = flag.Bool("verbose", false, "Print detailed execution info")
	preScript   = flag.String("pre-failover-script", "", "Path of pre-failover script")
	postScript  = flag.String("post-failover-script", "", "Path of post-failover script")
	maxDelay    = flag.Int64("maxdelay", 0, "Maximum replication delay before initiating failover")
	gtidCheck   = flag.Bool("gtidcheck", false, "Check that GTID sequence numbers are identical before initiating failover")
)

var (
	dbUser     string
	dbPass     string
	rplUser    string
	rplPass    string
	masterHost string
	masterPort string
)

type MasterMonitor struct {
	Host      string
	Port      string
	BinlogPos string
	Strict    string
}

type SlaveMonitor struct {
	Host      string
	Port      string
	LogBin    string
	UsingGtid string
	SlaveGtid string
	IOThread  string
	SQLThread string
	ReadOnly  string
	Delay     sql.NullInt64
}

func main() {
	flag.Parse()
	if *version == true {
		common.Version()
	}
	// if slaves option has been supplied, split into a slice.
	if *slaves != "" {
		slaveList = strings.Split(*slaves, ",")
	}
	if *masterUrl == "" {
		log.Fatal("ERROR: No master host specified.")
	}
	masterHost, masterPort = splitHostPort(*masterUrl)
	masterIP, err := dbhelper.CheckHostAddr(masterHost)
	if err != nil {
		log.Fatalln("ERROR: DNS resolution error for host", masterHost)
	}
	if *user == "" {
		log.Fatal("ERROR: No master user/pair specified.")
	}
	dbUser, dbPass = splitPair(*user)
	if *rpluser == "" {
		log.Fatal("ERROR: No replication user/pair specified.")
	}
	rplUser, rplPass = splitPair(*rpluser)
	if *verbose {
		log.Printf("Connecting to master server %s:%s", masterHost, masterPort)
	}

	master, err = dbhelper.MySQLConnect(dbUser, dbPass, dbhelper.GetAddress(masterHost, masterPort, *socket))
	if err != nil {
		log.Fatal("Error: could not connect to master server.")
	}
	defer master.Close()
	// If slaves option is empty, then attempt automatic discovery.
	// fmt.Println("Length of slaveList", len(slaveList))
	if len(slaveList) == 0 {
		slaveList = dbhelper.GetSlaveHostsDiscovery(master)
		if len(slaveList) == 0 {
			log.Fatal("Error: no slaves found. Please supply a list of slaves manually.")
		}
	}
	for _, v := range slaveList {
		slaveHost, slavePort := splitHostPort(v)
		slaveIP, err := dbhelper.CheckHostAddr(slaveHost)
		if err != nil {
			log.Fatalln("ERROR: DNS resolution error for host", slaveHost)
		}
		if validateHostPort(slaveIP, slavePort) {
			var err error
			slave, err := dbhelper.MySQLConnect(dbUser, dbPass, dbhelper.GetAddress(slaveHost, slavePort, *socket))
			if err != nil {
				log.Fatal(err)
			}
			if *verbose {
				log.Printf("Checking if server %s is a slave of server %s", slaveHost, masterHost)
			}
			if dbhelper.IsSlaveof(slave, slaveHost, masterIP) == false {
				log.Fatalf("ERROR: Server %s is not a slave of declared master %s", v, masterHost)
			}
			slave.Close()
		}
	}

	err = termbox.Init()
	if err != nil {
		log.Fatal(err)
	}
	defer termbox.Close()
	termboxChan := new_tb_chan()
	interval := time.Second
	ticker := time.NewTicker(interval * 3)
	drawMonitor()
Loop:
	for exit == false {
		select {
		case <-ticker.C:
			status = dbhelper.GetStatusAsInt(master)
			variable = dbhelper.GetVariables(master)
			drawMonitor()
		case event := <-termboxChan:
			switch event.Type {
			case termbox.EventKey:
				if event.Key == termbox.KeyCtrlS {
					exit = true
					ticker.Stop()
					close(termboxChan)
					termbox.Close()
					switchover()
					log.Println("Quitting")
					goto Loop
				}
				if event.Key == termbox.KeyCtrlQ {
					exit = true
				}
			}
			switch event.Ch {
			case 's':
				termbox.Sync()
			}
		}
	}
}

func drawMonitor() {
	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	printTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, "MariaDB Replication Monitor and Health Checker")
	m := new(MasterMonitor)
	m.init()
	printfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %20s %12s", "Master Host", "Port", "Binlog Position", "Strict Mode")
	printfTb(0, 3, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %20s %12s", m.Host, m.Port, m.BinlogPos, m.Strict)
	printfTb(0, 5, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %7s %12s %20s %20s %6s %3s", "Slave Host", "Port", "Binlog", "Using GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	vy := 6
	for _, v := range slaveList {
		slave := new(SlaveMonitor)
		slave.init(v)
		printfTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %7s %12s %20s %20s %6d %3s", slave.Host, slave.Port, slave.LogBin, slave.UsingGtid, slave.SlaveGtid, slave.healthCheck(), slave.Delay.Int64, slave.ReadOnly)
		vy++
	}
	vy += 2
	printTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, "   Ctrl-Q to quit, Ctrl-S to switch over")
	termbox.Flush()
	time.Sleep(time.Duration(1) * time.Second)
}

/* Init a monitored master object */
func (mm *MasterMonitor) init() {
	mm.Host = masterHost
	mm.Port = masterPort
	mm.BinlogPos = dbhelper.GetVariableByName(master, "GTID_BINLOG_POS")
	mm.Strict = dbhelper.GetVariableByName(master, "GTID_STRICT_MODE")
}

/* Init a monitored slave object */
func (sm *SlaveMonitor) init(url string) error {
	sm.Host, sm.Port = splitHostPort(url)
	slave, err := dbhelper.MySQLConnect(dbUser, dbPass, "tcp("+url+")")
	defer slave.Close()
	if err != nil {
		return err
	}
	slaveStatus, err := dbhelper.GetSlaveStatus(slave)
	if err != nil {
		return err
	}
	sm.LogBin = dbhelper.GetVariableByName(slave, "LOG_BIN")
	sm.ReadOnly = dbhelper.GetVariableByName(slave, "READ_ONLY")
	sm.SlaveGtid = dbhelper.GetVariableByName(slave, "GTID_SLAVE_POS")
	sm.UsingGtid = slaveStatus.Using_Gtid
	sm.IOThread = slaveStatus.Slave_IO_Running
	sm.SQLThread = slaveStatus.Slave_SQL_Running
	sm.Delay = slaveStatus.Seconds_Behind_Master
	return err
}

/* Check replication health and return status string */
func (sm *SlaveMonitor) healthCheck() string {
	if sm.Delay.Valid == false {
		if sm.SQLThread == "Yes" && sm.IOThread == "No" {
			return "NOT OK, IO Stopped"
		} else if sm.SQLThread == "No" && sm.IOThread == "Yes" {
			return "NOT OK, SQL Stopped"
		} else {
			return "NOT OK, ALL Stopped"
		}
	} else {
		if sm.Delay.Int64 > 0 {
			return "Behind master"
		}
		return "Running OK"
	}
}

func switchover() {
	log.Println("Starting switchover")
	log.Println("Flushing tables on master")
	err := dbhelper.FlushTablesNoLog(master)
	if err != nil {
		log.Println("WARNING: Could not flush tables on master", err)
	}
	log.Println("Checking long running updates on master")
	if dbhelper.CheckLongRunningWrites(master, 10) > 0 {
		log.Fatal("ERROR: Long updates running on master. Cannot switchover")
	}
	log.Println("Electing a new master")
	candidate := electCandidate(slaveList)
	newMasterHost, newMasterPort := splitHostPort(candidate)
	log.Printf("Slave %s has been elected as a new master", candidate)
	if *preScript != "" {
		log.Printf("Calling pre-failover script")
		out, err := exec.Command(*preScript, masterHost, newMasterHost).CombinedOutput()
		if err != nil {
			log.Println("ERROR:", err)
		}
		log.Println("Post-failover script complete", string(out))
	}
	log.Printf("Rejecting updates on master")
	err = dbhelper.FlushTablesWithReadLock(master)
	if err != nil {
		log.Println("WARNING: Could not lock tables on master", err)
	}
	log.Println("Switching master")
	newMasterIP, err := dbhelper.CheckHostAddr(newMasterHost)
	if err != nil {
		log.Fatalln("ERROR: DNS resolution error for host", newMasterHost)
	}
	newMaster := dbhelper.Connect(dbUser, dbPass, "tcp("+candidate+")")
	log.Println("Waiting for candidate master to synchronize")
	masterGtid := dbhelper.GetVariableByName(master, "GTID_BINLOG_POS")
	dbhelper.MasterPosWait(newMaster, masterGtid)
	log.Println("Stopping slave thread on new master")
	err = dbhelper.StopSlave(newMaster)
	if err != nil {
		log.Println("WARNING: Stopping slave failed on new master")
	}
	cm := "CHANGE MASTER TO master_host='" + newMasterIP + "', master_port=" + newMasterPort + ", master_user='" + rplUser + "', master_password='" + rplPass + "'"
	log.Println("Switching old master as a slave")
	err = dbhelper.UnlockTables(master)
	if err != nil {
		log.Println("WARNING: Could not unlock tables on old master", err)
	}
	_, err = master.Exec(cm + ", master_use_gtid=current_pos")
	if err != nil {
		log.Println("WARNING: Change master failed on old master", err)
	}
	err = dbhelper.StartSlave(master)
	if err != nil {
		log.Println("WARNING: Start slave failed on old master", err)
	}
	err = dbhelper.SetReadOnly(master, true)
	if err != nil {
		log.Printf("ERROR: Could not set old master as read-only", err)
	}
	log.Println("Resetting slave on new master and set read/write mode on")
	err = dbhelper.ResetSlave(newMaster, true)
	if err != nil {
		log.Println("WARNING: Reset slave failed on new master")
	}
	err = dbhelper.SetReadOnly(newMaster, false)
	if err != nil {
		log.Println("ERROR: Could not set new master as read-write")
	}
	log.Println("Switching other slaves to the new master")
	for _, v := range slaveList {
		if v == candidate {
			continue
		}
		slaveHost, slavePort := splitHostPort(v)
		slave, err := dbhelper.MySQLConnect(dbUser, dbPass, dbhelper.GetAddress(slaveHost, slavePort, *socket))
		if err != nil {
			log.Printf("ERROR: Could not connect to slave %s, %s", v, err)
		} else {
			log.Printf("Waiting for slave %s to sync", v)
			dbhelper.MasterPosWait(newMaster, masterGtid)
			log.Printf("Change master on slave %s", v)
			err := dbhelper.StopSlave(slave)
			if err != nil {
				log.Printf("WARNING: Could not stop slave on server %s, %s", v, err)
			}
			_, err = slave.Exec(cm)
			if err != nil {
				log.Printf("ERROR: Change master failed on slave %s, %s", v, err)
			}
			err = dbhelper.StartSlave(slave)
			if err != nil {
				log.Printf("ERROR: could not start slave on server %s, %s", v, err)
			}
			err = dbhelper.SetReadOnly(slave, true)
			if err != nil {
				log.Printf("ERROR: Could not set slave %s as read-only, %s", v, err)
			}
		}
	}
	if *postScript != "" {
		log.Printf("Calling post-failover script")
		out, err := exec.Command(*postScript, masterHost, newMasterHost).CombinedOutput()
		if err != nil {
			log.Println("ERROR:", err)
		}
		log.Println("Post-failover script complete", string(out))
	}
	log.Println("Switchover complete")
	return
}

/* Returns two host and port items from a pair, e.g. host:port */
func splitHostPort(s string) (string, string) {
	items := strings.Split(s, ":")
	if len(items) == 1 {
		return items[0], "3306"
	} else {
		return items[0], items[1]
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

/* Validate server host and port */
func validateHostPort(h string, p string) bool {
	if net.ParseIP(h) == nil {
		return false
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		/* Not an integer */
		return false
	}
	if port > 0 && port <= 65535 {
		return true
	} else {
		return false
	}
}

/* Returns a candidate from a list of slaves. If there's only one slave it will be the de facto candidate. */
func electCandidate(l []string) string {
	ll := len(l)
	if *verbose {
		log.Println("Processing %s candidates", ll)
	}
	seqList := make([]uint64, ll)
	i := 0
	hiseq := 0
	for _, v := range l {
		if *verbose {
			log.Println("Connecting to slave", v)
		}
		sl, err := dbhelper.MySQLConnect(dbUser, dbPass, "tcp("+v+")")
		if err != nil {
			log.Printf("WARNING: Server %s not online. Skipping", v)
			continue
		}
		sh, _ := splitHostPort(v)
		if dbhelper.CheckSlavePrerequisites(sl, sh) == false {
			continue
		}
		if dbhelper.CheckBinlogFilters(master, sl) == false {
			log.Printf("WARNING: Binlog filters differ on master and slave %s. Skipping", v)
			continue
		}
		if dbhelper.CheckReplicationFilters(master, sl) == false {
			log.Printf("WARNING: Replication filters differ on master and slave %s. Skipping", v)
			continue
		}
		ss, err := dbhelper.GetSlaveStatus(sl)
		if ss.Seconds_Behind_Master.Valid == false {
			log.Printf("WARNING: Slave %s is stopped. Skipping", v)
			continue
		}
		if ss.Seconds_Behind_Master.Int64 > *maxDelay {
			log.Printf("WARNING: Slave %s has more than %d seconds of replication delay (%d). Skipping", v, *maxDelay, ss.Seconds_Behind_Master.Int64)
			continue
		}
		if *gtidCheck && dbhelper.CheckSlaveSync(sl, master) == false {
			log.Printf("WARNING: Slave %s not in sync. Skipping", v)
			continue
		}
		seqList[i] = getSeqFromGtid(dbhelper.GetVariableByName(sl, "GTID_CURRENT_POS"))
		var max uint64
		if i == 0 {
			max = seqList[0]
		} else if seqList[i] > max {
			max = seqList[i]
			hiseq = i
		}
		sl.Close()
		i++
	}
	if i > 0 {
		/* Return the slave with the highest seqno. */
		return l[hiseq]
	} else {
		log.Fatal("ERROR: No suitable candidates found.")
		return "err"
	}
}

func getSeqFromGtid(gtid string) uint64 {
	e := strings.Split(gtid, "-")
	s, _ := strconv.ParseUint(e[2], 10, 64)
	return s
}

func printTb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func printfTb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	printTb(x, y, fg, bg, s)
}

func new_tb_chan() chan termbox.Event {
	termboxChan := make(chan termbox.Event)
	go func() {
		for {
			termboxChan <- termbox.PollEvent()
		}
	}()
	return termboxChan
}
