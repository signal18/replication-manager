// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Author: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/nsf/termbox-go"
	"github.com/tanji/mariadb-tools/dbhelper"
)

const repmgrVersion string = "0.5.2-dev"

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
	switchOptions = []string{"keep", "kill"}
	failOptions   = []string{"monitor", "force", "check"}
	failCount     int
	failoverCtr   int
	tlog          TermLog
	ignoreList    []string
	logPtr        *os.File
)

// Command specific options
var (
	version     = flag.Bool("version", false, "Return version")
	user        = flag.String("user", "", "User for MariaDB login, specified in the [user]:[password] format")
	hosts       = flag.String("hosts", "", "List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	socket      = flag.String("socket", "/var/run/mysqld/mysqld.sock", "Path of MariaDB unix socket")
	rpluser     = flag.String("rpluser", "", "Replication user in the [user]:[password] format")
	interactive = flag.Bool("interactive", true, "Ask for user interaction when failures are detected")
	verbose     = flag.Bool("verbose", false, "Print detailed execution info")
	preScript   = flag.String("pre-failover-script", "", "Path of pre-failover script")
	postScript  = flag.String("post-failover-script", "", "Path of post-failover script")
	maxDelay    = flag.Int64("maxdelay", 0, "Maximum replication delay before initiating failover")
	gtidCheck   = flag.Bool("gtidcheck", false, "Check that GTID sequence numbers are identical before initiating failover")
	prefMaster  = flag.String("prefmaster", "", "Preferred candidate server for master failover, in host:[port] format")
	ignoreSrv   = flag.String("ignore-servers", "", "List of servers to ignore in slave promotion operations")
	waitKill    = flag.Int64("wait-kill", 5000, "Wait this many milliseconds before killing threads on demoted master")
	readonly    = flag.Bool("readonly", true, "Set slaves as read-only after switchover")
	failover    = flag.String("failover", "", "Failover mode, either 'monitor', 'force' or 'check'")
	maxfail     = flag.Int("failcount", 5, "Trigger failover after N failures (interval 1s)")
	switchover  = flag.String("switchover", "", "Switchover mode, either 'keep' or 'kill' the old master.")
	autorejoin  = flag.Bool("autorejoin", true, "Automatically rejoin a failed server to the current master.")
	logfile     = flag.String("logfile", "", "Write MRM messages to a log file")
	timeout     = flag.Int("connect-timeout", 5, "Database connection timeout in seconds")
	faillimit   = flag.Int("failover-limit", 0, "In interactive monitor mode, quit after N failovers (0: unlimited)")
	failtime    = flag.Int("failover-time-limit", 0, "")
)

const (
	stateFailed string = "Failed"
	stateMaster string = "Master"
	stateSlave  string = "Slave"
	stateUnconn string = "Unconnected"
)

func main() {
	var errLog = mysql.Logger(log.New(ioutil.Discard, "", 0))
	mysql.SetLogger(errLog)
	flag.Parse()
	if *version == true {
		fmt.Println("MariaDB Replication Manager version", repmgrVersion)
	}
	if *logfile != "" {
		var err error
		logPtr, err = os.Create(*logfile)
		if err != nil {
			log.Println("ERROR: Error opening logfile, disabling for the rest of the session.")
			*logfile = ""
		}
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
	// Forced failover implies interactive == false
	if *failover == "force" && *interactive == true {
		*interactive = false
	}

	if *ignoreSrv != "" {
		ignoreList = strings.Split(*ignoreSrv, ",")
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
			if driverErr, ok := err.(*mysql.MySQLError); ok {
				if driverErr.Number == 1045 {
					log.Fatalln("ERROR: Database access denied:", err.Error())
				}
			}
			if *verbose {
				log.Println("ERROR:", err)
			}
			log.Printf("INFO : Server %s is dead.", servers[k].URL)
			servers[k].State = stateFailed
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
			servers[k].State = stateSlave
			slaves = append(slaves, servers[k])
			slaveCount++
		} else {
			if *verbose {
				log.Printf("DEBUG: Server %s is not a slave. Setting aside", servers[k].URL)
				servers[k].State = stateUnconn
			}
		}
	}

	// If no slaves are detected, then bail out
	if len(slaves) == 0 {
		log.Fatal("ERROR: No slaves were detected.")
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
		sid := slaves[0].MasterServerID
		for k, s := range servers {
			if s.State == stateUnconn {
				if s.ServerID == sid {
					master = servers[k]
					master.State = stateMaster
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
			if s.State == stateFailed {
				if s.Host == smh || s.IP == smh {
					master = servers[k]
					master.State = stateMaster
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
		if *switchover != "" || *failover == "monitor" {
			log.Fatalln("ERROR: Could not autodetect a master!")
		} else {
			log.Fatalln("ERROR: Could not autodetect a failed master!")
		}
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
		masterFailover(true)
	} else if *switchover != "" && *interactive == false {
		masterFailover(false)
	} else {
		err := termbox.Init()
		if err != nil {
			log.Fatalln("Termbox initialization error", err)
		}
		tlog = NewTermLog(30)
		if *failover != "" {
			tlog.Add("Monitor started in failover mode")
		} else {
			tlog.Add("Monitor started in switchover mode")
		}
		termboxChan := newTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * 1)
		for exit == false {
			select {
			case <-ticker.C:
				display()
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						masterFailover(false)
					}
					if event.Key == termbox.KeyCtrlF {
						if master.State != stateFailed {
							masterFailover(true)
						}
					}
					if event.Key == termbox.KeyCtrlD {
						for k, v := range servers {
							logprint("Servers", k, v)
						}
						logprint("Master", master)
						for k, v := range slaves {
							logprint("Slaves", k, v)
						}
					}
					if event.Key == termbox.KeyCtrlR {
						logprint("INFO: Setting slaves read-only")
						for _, sl := range slaves {
							dbhelper.SetReadOnly(sl.Conn, true)
						}
					}
					if event.Key == termbox.KeyCtrlW {
						logprint("INFO: Setting slaves read-write")
						for _, sl := range slaves {
							dbhelper.SetReadOnly(sl.Conn, false)
						}
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
			if master.State == stateFailed && *interactive == false {
				masterFailover(true)
				if failoverCtr == *maxfail {
					exit = true
				}
			}
		}
		termbox.Close()
	}
}

func newTbChan() chan termbox.Event {
	termboxChan := make(chan termbox.Event)
	go func() {
		for {
			termboxChan <- termbox.PollEvent()
		}
	}()
	return termboxChan
}
