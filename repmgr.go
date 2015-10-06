package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/nsf/termbox-go"
	"github.com/tanji/mariadb-tools/dbhelper"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const repmgrVersion string = "0.5.0-dev"

var (
	hostList      []string
	servers       []*ServerMonitor
	slaves        []*ServerMonitor
	master        *ServerMonitor
	exit          bool
	vy            int
	dbUser        string
	dbPass        string
	rplUser       string
	rplPass       string
	switchOptions     = []string{"keep", "kill"}
	failOptions       = []string{"monitor", "force", "check"}
	failCount     int = 0
)

var (
	version = flag.Bool("version", false, "Return version")
	user    = flag.String("user", "", "User for MariaDB login, specified in the [user]:[password] format")
	hosts   = flag.String("hosts", "", "List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	socket  = flag.String("socket", "/var/run/mysqld/mysqld.sock", "Path of MariaDB unix socket")
	rpluser = flag.String("rpluser", "", "Replication user in the [user]:[password] format")
	// command specific-options
	interactive = flag.Bool("interactive", true, "Ask for user interaction when failures are detected")
	verbose     = flag.Bool("verbose", false, "Print detailed execution info")
	preScript   = flag.String("pre-failover-script", "", "Path of pre-failover script")
	postScript  = flag.String("post-failover-script", "", "Path of post-failover script")
	maxDelay    = flag.Int64("maxdelay", 0, "Maximum replication delay before initiating failover")
	gtidCheck   = flag.Bool("gtidcheck", false, "Check that GTID sequence numbers are identical before initiating failover")
	prefMaster  = flag.String("prefmaster", "", "Preferred candidate server for master failover, in host:[port] format")
	waitKill    = flag.Int64("wait-kill", 5000, "Wait this many milliseconds before killing threads on demoted master")
	readonly    = flag.Bool("readonly", true, "Set slaves as read-only after switchover")
	failover    = flag.String("failover", "", "Failover mode, either 'monitor', 'force' or 'check'")
	switchover  = flag.String("switchover", "", "Switchover mode, either 'keep' or 'kill' the old master.")
)

type TermLog []string

var tlog TermLog

type ServerMonitor struct {
	Conn           *sqlx.DB
	URL            string
	Host           string
	Port           string
	IP             string
	BinlogPos      string
	Strict         string
	ServerId       uint
	MasterServerId uint
	MasterHost     string
	LogBin         string
	UsingGtid      string
	CurrentGtid    string
	SlaveGtid      string
	IOThread       string
	SQLThread      string
	ReadOnly       string
	Delay          sql.NullInt64
	State          string
}

const (
	STATE_FAILED string = "Failed"
	STATE_MASTER string = "Master"
	STATE_SLAVE  string = "Slave"
	STATE_UNCONN string = "Unconnected"
)

func main() {
	flag.Parse()
	if *version == true {
		fmt.Println("MariaDB Replication Manager version", repmgrVersion)
	}
	// if slaves option has been supplied, split into a slice.
	if *hosts != "" {
		hostList = strings.Split(*hosts, ",")
	} else {
		log.Fatal("ERROR: No hosts list specified.")
	}
	// validate users.
	if *user == "" {
		log.Fatal("ERROR: No master user/pair specified.")
	}
	dbUser, dbPass = splitPair(*user)
	if *rpluser == "" {
		log.Fatal("ERROR: No replication user/pair specified.")
	}
	rplUser, rplPass = splitPair(*rpluser)

	// Check that failover and switchover modes are set correctly.
	if *switchover == "" && *failover == "" {
		log.Fatal("ERROR: None of the switchover or failover modes are set.")
	}
	if *switchover != "" && *failover != "" {
		log.Fatal("ERROR: Both switchover and failover modes are set.")
	}
	if !contains(failOptions, *failover) && *failover != "" {
		log.Fatalf("ERROR: Incorrect failover mode: %s", *failover)
	}
	if !contains(switchOptions, *switchover) && *switchover != "" {
		log.Fatalf("ERROR: Incorrect switchover mode: %s", *switchover)
	}

	// Create a connection to each host and build list of slaves.
	hostCount := len(hostList)
	servers = make([]*ServerMonitor, hostCount)
	slaveCount := 0
	for k, url := range hostList {
		var err error
		servers[k], err = newServerMonitor(url)
		if *verbose {
			log.Printf("DEBUG: Creating new server: %v", servers[k].URL)
		}
		if err != nil {
			log.Printf("INFO : Server %s is dead.", servers[k].URL)
			servers[k].State = STATE_FAILED
			continue
		}
		defer servers[k].Conn.Close()
		if *verbose {
			log.Printf("DEBUG: Checking if server %s is slave", servers[k].URL)
		}

		servers[k].refresh()
		if servers[k].UsingGtid != "" {
			if *verbose {
				log.Printf("DEBUG: Server %s is configured as a slave", servers[k].URL)
			}
			servers[k].State = STATE_SLAVE
			slaves = append(slaves, servers[k])
			slaveCount++
		} else {
			if *verbose {
				log.Printf("DEBUG: Server %s is not a slave. Setting aside", servers[k].URL)
			}
		}
	}

	// Check that all slave servers have the same master.
	for _, sl := range slaves {
		if sl.hasSiblings(slaves) == false {
			log.Fatalln("ERROR: Multi-master topologies are not yet supported.")
		}
	}

	// Depending if we are doing a failover or a switchover, we will find the master in the list of
	// dead hosts or unconnected hosts.
	if *switchover != "" || *failover == "monitor" {
		// First of all, get a server id from the slaves slice, they should be all the same
		sid := slaves[0].MasterServerId
		for k, s := range servers {
			if s.State == STATE_UNCONN {
				if s.ServerId == sid {
					master = servers[k]
					master.State = STATE_MASTER
					if *verbose {
						log.Printf("DEBUG: Server %s was autodetected as a master", s.URL)
					}
					break
				}
			}
		}
	} else {
		// Slave master_host variable must point to dead master
		smh := slaves[0].MasterHost
		for k, s := range servers {
			if s.State == STATE_FAILED {
				if s.Host == smh || s.IP == smh {
					master = servers[k]
					master.State = STATE_MASTER
					if *verbose {
						log.Printf("DEBUG: Server %s was autodetected as a master", s.URL)
					}
					break
				}
			}
		}
	}
	// Final check if master has been found
	if master == nil {
		log.Fatalln("ERROR: Could not autodetect a master!")
	}

	for _, sl := range slaves {
		if *verbose {
			log.Printf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, master.Host)
		}
		if dbhelper.IsSlaveof(sl.Conn, sl.Host, master.IP) == false {
			log.Printf("WARN : Server %s is not a slave of declared master %s", master.URL, master.Host)
		}
	}

	// Check if preferred master is included in Host List
	ret := func() bool {
		for _, v := range hostList {
			if v == *prefMaster {
				return true
			}
		}
		return false
	}
	if ret() == false && *prefMaster != "" {
		log.Fatal("ERROR: Preferred master is not included in the hosts option")
	}

	// Do failover or switchover manually, or start the interactive monitor.

	if *failover == "force" {
		master.failover()
	} else if *switchover != "" && *interactive == false {
		master.switchover()
	} else {
	MainLoop:
		err := termbox.Init()
		if err != nil {
			log.Fatalln("Termbox initialization error", err)
		}
		tlog = NewTermLog(20)
		if *failover != "" {
			tlog.Add("Monitor started in failover mode")
		} else {
			tlog.Add("Monitor started in switchover mode")
		}
		termboxChan := new_tb_chan()
		interval := time.Second
		ticker := time.NewTicker(interval * 3)
		var command string
		for exit == false {
			select {
			case <-ticker.C:
				display()
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						nmUrl, nsKey := master.switchover()
						if nmUrl != "" && nsKey >= 0 {
							if *verbose {
								logprintf("DEBUG: Reinstancing new master: %s and new slave: %s [%d]", nmUrl, slaves[nsKey].URL, nsKey)
							}
							master, err = newServerMonitor(nmUrl)
							slaves[nsKey], err = newServerMonitor(slaves[nsKey].URL)
						}
					}
					if event.Key == termbox.KeyCtrlF {
						command = "failover"
						exit = true
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
			if master.State == STATE_FAILED && *interactive == false {
				command = "failover"
				exit = true
			}
		}
		switch command {
		case "failover":
			termbox.Close()
			nmUrl, nmKey := master.failover()
			if nmUrl != "" {
				if *verbose {
					log.Printf("DEBUG: Reinstancing new master: %s", nmUrl)
				}
				master, err = newServerMonitor(nmUrl)
				// Remove new master from slave slice
				slaves = append(slaves[:nmKey], slaves[nmKey+1:]...)
			}
			log.Println("###### Restarting monitor console in 5 seconds. Press Ctrl-C to exit")
			time.Sleep(5 * time.Second)
			exit = false
			goto MainLoop
		}
		termbox.Close()
	}
}

/* Initializes a server object */
func newServerMonitor(url string) (*ServerMonitor, error) {
	server := new(ServerMonitor)
	server.URL = url
	server.Host, server.Port = splitHostPort(url)
	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		return server, errors.New(fmt.Sprintf("ERROR: DNS resolution error for host %s", server.Host))
	}
	server.Conn, err = dbhelper.MySQLConnect(dbUser, dbPass, dbhelper.GetAddress(server.Host, server.Port, *socket))
	if err != nil {
		server.State = STATE_FAILED
		return server, errors.New(fmt.Sprintf("ERROR: could not connect to server %s: %s", url, err))
	}
	server.State = STATE_UNCONN
	return server, nil
}

/* Refresh a server object */
func (sm *ServerMonitor) refresh() error {
	err := sm.Conn.Ping()
	if err != nil {
		return err
	}
	sv, err := dbhelper.GetVariables(sm.Conn)
	if err != nil {
		return err
	}
	sm.BinlogPos = sv["GTID_BINLOG_POS"]
	sm.Strict = sv["GTID_STRICT_MODE"]
	sm.LogBin = sv["LOG_BIN"]
	sm.ReadOnly = sv["READ_ONLY"]
	sm.CurrentGtid = sv["GTID_CURRENT_POS"]
	sm.SlaveGtid = sv["GTID_SLAVE_POS"]
	sid, _ := strconv.ParseUint(sv["SERVER_ID"], 10, 0)
	sm.ServerId = uint(sid)
	slaveStatus, err := dbhelper.GetSlaveStatus(sm.Conn)
	if err != nil {
		return err
	}
	sm.UsingGtid = slaveStatus.Using_Gtid
	sm.IOThread = slaveStatus.Slave_IO_Running
	sm.SQLThread = slaveStatus.Slave_SQL_Running
	sm.Delay = slaveStatus.Seconds_Behind_Master
	sm.MasterServerId = slaveStatus.Master_Server_Id
	sm.MasterHost = slaveStatus.Master_Host
	return err
}

/* Check replication health and return status string */
func (sm *ServerMonitor) healthCheck() string {
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

/* Triggers a master switchover. Returns the new master's URL */
func (master *ServerMonitor) switchover() (string, int) {
	logprint("INFO : Starting switchover")
	// Phase 1: Cleanup and election
	logprintf("INFO : Flushing tables on %s (master)", master.URL)
	err := dbhelper.FlushTablesNoLog(master.Conn)
	if err != nil {
		logprintf("WARN : Could not flush tables on master", err)
	}
	logprint("INFO : Checking long running updates on master")
	if dbhelper.CheckLongRunningWrites(master.Conn, 10) > 0 {
		logprint("ERROR: Long updates running on master. Cannot switchover")
		return "", -1
	}
	logprint("INFO : Electing a new master")
	var nmUrl string
	key := master.electCandidate(slaves)
	if key == -1 {
		return "", -1
	}
	nmUrl = slaves[key].URL
	logprintf("INFO : Slave %s has been elected as a new master", nmUrl)
	newMaster, err := newServerMonitor(nmUrl)
	if *preScript != "" {
		logprintf("INFO : Calling pre-failover script")
		out, err := exec.Command(*preScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Pre-failover script complete:", string(out))
	}
	// Phase 2: Reject updates and sync slaves
	master.freeze()
	logprintf("INFO : Rejecting updates on %s (old master)", master.URL)
	err = dbhelper.FlushTablesWithReadLock(master.Conn)
	if err != nil {
		logprintf("WARN : Could not lock tables on %s (old master) %s", master.URL, err)
	}
	logprint("INFO : Switching master")
	logprint("INFO : Waiting for candidate master to synchronize")
	masterGtid := dbhelper.GetVariableByName(master.Conn, "GTID_BINLOG_POS")
	if *verbose {
		logprintf("DEBUG: Syncing on master GTID Current Pos [%s]", masterGtid)
		master.log()
	}
	dbhelper.MasterPosWait(newMaster.Conn, masterGtid)
	if *verbose {
		logprint("DEBUG: MASTER_POS_WAIT executed.")
		newMaster.log()
	}
	// Phase 3: Prepare new master
	logprint("INFO : Stopping slave thread on new master")
	err = dbhelper.StopSlave(newMaster.Conn)
	if err != nil {
		logprint("WARN : Stopping slave failed on new master")
	}
	// Call post-failover script before unlocking the old master.
	if *postScript != "" {
		logprintf("INFO : Calling post-failover script")
		out, err := exec.Command(*postScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Post-failover script complete", string(out))
	}
	logprint("INFO : Resetting slave on new master and set read/write mode on")
	err = dbhelper.ResetSlave(newMaster.Conn, true)
	if err != nil {
		logprint("WARN : Reset slave failed on new master")
	}
	err = dbhelper.SetReadOnly(newMaster.Conn, false)
	if err != nil {
		logprint("ERROR: Could not set new master as read-write")
	}
	newGtid := dbhelper.GetVariableByName(master.Conn, "GTID_BINLOG_POS")
	// Insert a bogus transaction in order to have a new GTID pos on master
	err = dbhelper.FlushTables(newMaster.Conn)
	if err != nil {
		logprint("WARN : Could not flush tables on new master", err)
	}
	// Phase 4: Demote old master to slave
	cm := "CHANGE MASTER TO master_host='" + newMaster.IP + "', master_port=" + newMaster.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "'"
	logprint("INFO : Switching old master as a slave")
	err = dbhelper.UnlockTables(master.Conn)
	if err != nil {
		logprint("WARN : Could not unlock tables on old master", err)
	}
	dbhelper.StopSlave(master.Conn) // This is helpful because in some cases the old master can have an old configuration running
	_, err = master.Conn.Exec("SET GLOBAL gtid_slave_pos='" + newGtid + "'")
	if err != nil {
		logprint("WARN : Could not set gtid_slave_pos on old master", err)
	}
	_, err = master.Conn.Exec(cm + ", master_use_gtid=slave_pos")
	if err != nil {
		logprint("WARN : Change master failed on old master", err)
	}
	err = dbhelper.StartSlave(master.Conn)
	if err != nil {
		logprint("WARN : Start slave failed on old master", err)
	}
	if *readonly {
		err = dbhelper.SetReadOnly(master.Conn, true)
		if err != nil {
			logprintf("ERROR: Could not set old master as read-only, %s", err)
		}
	}
	// Phase 5: Switch slaves to new master
	logprint("INFO : Switching other slaves to the new master")
	var oldMasterKey int
	for k, sl := range slaves {
		if sl.URL == newMaster.URL {
			slaves[k].URL = master.URL
			oldMasterKey = k
			if *verbose {
				logprintf("DEBUG: New master %s found in slave slice at key %d, reinstancing URL to %s", sl.URL, k, master.URL)
			}
			continue
		}
		logprintf("INFO : Waiting for slave %s to sync", sl.URL)
		dbhelper.MasterPosWait(sl.Conn, masterGtid)
		if *verbose {
			sl.log()
		}
		logprintf("INFO : Change master on slave %s", sl.URL)
		err := dbhelper.StopSlave(sl.Conn)
		if err != nil {
			logprintf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		_, err = sl.Conn.Exec("SET GLOBAL gtid_slave_pos='" + newGtid + "'")
		if err != nil {
			logprintf("WARN : Could not set gtid_slave_pos on slave %s, %s", sl.URL, err)
		}
		_, err = sl.Conn.Exec(cm)
		if err != nil {
			logprintf("ERROR: Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			logprintf("ERROR: could not start slave on server %s, %s", sl.URL, err)
		}
		if *readonly {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				logprintf("ERROR: Could not set slave %s as read-only, %s", sl.URL, err)
			}
		}
	}
	logprint("INFO : Switchover complete")
	return newMaster.URL, oldMasterKey
}

/* Triggers a master failover. Returns the new master's URL and key */
func (master *ServerMonitor) failover() (string, int) {
	log.Println("INFO : Starting failover and electing a new master")
	var nmUrl string
	key := master.electCandidate(slaves)
	if key == -1 {
		return "", -1
	}
	nmUrl = slaves[key].URL
	log.Printf("INFO : Slave %s has been elected as a new master", nmUrl)
	newMaster, err := newServerMonitor(nmUrl)
	if *preScript != "" {
		log.Printf("INFO : Calling pre-failover script")
		out, err := exec.Command(*preScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			log.Println("ERROR:", err)
		}
		log.Println("INFO : Post-failover script complete:", string(out))
	}
	log.Println("INFO : Switching master")
	log.Println("INFO : Stopping slave thread on new master")
	err = dbhelper.StopSlave(newMaster.Conn)
	if err != nil {
		log.Println("WARN : Stopping slave failed on new master")
	}
	cm := "CHANGE MASTER TO master_host='" + newMaster.IP + "', master_port=" + newMaster.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "'"
	log.Println("INFO : Resetting slave on new master and set read/write mode on")
	err = dbhelper.ResetSlave(newMaster.Conn, true)
	if err != nil {
		log.Println("WARN : Reset slave failed on new master")
	}
	err = dbhelper.SetReadOnly(newMaster.Conn, false)
	if err != nil {
		log.Println("ERROR: Could not set new master as read-write")
	}
	log.Println("INFO : Switching other slaves to the new master")
	for _, sl := range slaves {
		log.Printf("INFO : Change master on slave %s", sl.URL)
		err := dbhelper.StopSlave(sl.Conn)
		if err != nil {
			log.Printf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		_, err = sl.Conn.Exec(cm)
		if err != nil {
			log.Printf("ERROR: Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			log.Printf("ERROR: could not start slave on server %s, %s", sl.URL, err)
		}
		if *readonly {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				log.Printf("ERROR: Could not set slave %s as read-only, %s", sl.URL, err)
			}
		}
	}
	if *postScript != "" {
		log.Printf("INFO : Calling post-failover script")
		out, err := exec.Command(*postScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			log.Println("ERROR:", err)
		}
		log.Println("INFO : Post-failover script complete", string(out))
	}
	log.Println("INFO : Failover complete")
	return newMaster.URL, key
}

/* Handles write freeze and existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err != nil {
		logprintf("WARN : Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := *waitKill; i > 0; i -= 500 {
		threads := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		if threads == 0 {
			break
		}
		logprintf("INFO : Waiting for %d write threads to complete on %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	logprintf("INFO : Terminating all threads on %s", server.URL)
	dbhelper.KillThreads(server.Conn)
	return true
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
func (master *ServerMonitor) electCandidate(l []*ServerMonitor) int {
	ll := len(l)
	if *verbose {
		logprintf("DEBUG: Processing %d candidates", ll)
	}
	seqList := make([]uint64, ll)
	i := 0
	hiseq := 0
	for _, sl := range l {
		if *failover == "" {
			if *verbose {
				logprintf("DEBUG: Checking eligibility of slave server %s", sl.URL)
			}
			if dbhelper.CheckSlavePrerequisites(sl.Conn, sl.Host) == false {
				continue
			}
			if dbhelper.CheckBinlogFilters(master.Conn, sl.Conn) == false {
				logprintf("WARN : Binlog filters differ on master and slave %s. Skipping", sl.URL)
				continue
			}
			if dbhelper.CheckReplicationFilters(master.Conn, sl.Conn) == false {
				logprintf("WARN : Replication filters differ on master and slave %s. Skipping", sl.URL)
				continue
			}
			ss, _ := dbhelper.GetSlaveStatus(sl.Conn)
			if ss.Seconds_Behind_Master.Valid == false {
				logprintf("WARN : Slave %s is stopped. Skipping", sl.URL)
				continue
			}
			if ss.Seconds_Behind_Master.Int64 > *maxDelay {
				logprintf("WARN : Slave %s has more than %d seconds of replication delay (%d). Skipping", sl.URL, *maxDelay, ss.Seconds_Behind_Master.Int64)
				continue
			}
			if *gtidCheck && dbhelper.CheckSlaveSync(sl.Conn, master.Conn) == false {
				logprintf("WARN : Slave %s not in sync. Skipping", sl.URL)
				continue
			}
		}
		/* Rig the election if the examined slave is preferred candidate master */
		if sl.URL == *prefMaster {
			if *verbose {
				logprintf("DEBUG: Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}
		seqList[i] = getSeqFromGtid(dbhelper.GetVariableByName(sl.Conn, "GTID_CURRENT_POS"))
		var max uint64
		if i == 0 {
			max = seqList[0]
		} else if seqList[i] > max {
			max = seqList[i]
			hiseq = i
		}
		i++
	}
	if i > 0 {
		/* Return key of slave with the highest seqno. */
		return hiseq
	} else {
		log.Println("ERROR: No suitable candidates found.")
		return -1
	}
}

func (server *ServerMonitor) log() {
	server.refresh()
	logprintf("DEBUG: Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s\n", server.URL, server.CurrentGtid, server.SlaveGtid, server.BinlogPos)
	return
}

func getSeqFromGtid(gtid string) uint64 {
	e := strings.Split(gtid, "-")
	if len(e) != 3 {
		log.Fatalln("Error splitting GTID:", gtid)
	}
	s, err := strconv.ParseUint(e[2], 10, 64)
	if err != nil {
		log.Fatalln("Error getting sequence from GTID:", err)
	}
	return s
}

func display() {
	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" MariaDB Replication Monitor and Health Checker version %s ", repmgrVersion)
	if *failover != "" {
		headstr += " |  Mode: Failover "
	} else {
		headstr += " |  Mode: Switchover "
	}
	printfTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, headstr)
	printfTb(0, 5, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %7s %12s %20s %20s %20s %6s %3s", "Slave Host", "Port", "Binlog", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	// Check Master Status and print it out to terminal. Increment failure counter if needed.
	err := master.refresh()
	if err != nil && err != sql.ErrNoRows && failCount < 4 {
		failCount++
		tlog.Add(fmt.Sprintf("Master Failure detected! Retry %d/3", failCount))
		if failCount > 3 {
			tlog.Add("Declaring master as failed")
			master.State = STATE_FAILED
			master.CurrentGtid = "MASTER FAILED"
			master.BinlogPos = "MASTER FAILED"
		}
		termbox.Sync()
	}
	printfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %41s %20s %12s", "Master Host", "Port", "Current GTID", "Binlog Position", "Strict Mode")
	printfTb(0, 3, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %41s %20s %12s", master.Host, master.Port, master.CurrentGtid, master.BinlogPos, master.Strict)
	vy = 6
	for _, slave := range slaves {
		slave.refresh()
		printfTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %7s %12s %20s %20s %20s %6d %3s", slave.Host, slave.Port, slave.LogBin, slave.UsingGtid, slave.CurrentGtid, slave.SlaveGtid, slave.healthCheck(), slave.Delay.Int64, slave.ReadOnly)
		vy++
	}
	vy++
	for _, server := range servers {
		f := false
		if server.State == STATE_UNCONN {
			if f == false {
				printfTb(0, vy, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %41s %20s %12s", "Standalone Host", "Port", "Current GTID", "Binlog Position", "Strict Mode")
				f = true
				vy++
			}
			server.refresh()
			printfTb(0, vy, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %41s %20s %12s", "Master Host", "Port", "Current GTID", "Binlog Position", "Strict Mode")
			printfTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %41s %20s %12s", server.Host, server.Port, server.CurrentGtid, server.BinlogPos, server.Strict)
			vy++
		}

	}
	vy++
	if master.CurrentGtid != "MASTER FAILED" {
		printTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-S to switchover")
	} else {
		printTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-F to failover")
	}
	vy = vy + 3
	tlog.Print()
	termbox.Flush()
}

func (s *ServerMonitor) hasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		if s.MasterServerId != sl.MasterServerId {
			return false
		}
	}
	return true
}

func NewTermLog(sz int) TermLog {
	tl := make(TermLog, sz)
	return tl
}

func (tl *TermLog) Add(s string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	s = " " + ts + " " + s
	*tl = shift(*tl, s)
}

func (tl TermLog) Print() {
	for _, line := range tl {
		printTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, line)
		vy++
	}
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

func logprint(msg ...interface{}) {
	if *interactive == true || *failover == "monitor" {
		tlog.Add(fmt.Sprintln(msg...))
		display()
	} else {
		log.Println(msg...)
	}
}

func logprintf(format string, args ...interface{}) {
	if *interactive == true || *failover == "monitor" {
		tlog.Add(fmt.Sprintf(format, args...))
		display()
	} else {
		log.Printf(format, args...)
	}
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

func shift(s []string, e string) []string {
	ns := make([]string, 1)
	ns[0] = e
	ns = append(ns, s[0:20]...)
	return ns
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
