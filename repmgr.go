// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Author: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/nsf/termbox-go"
	"github.com/spf13/cobra"
	"github.com/tanji/mariadb-tools/dbhelper"
)

const repmgrVersion string = "0.7-dev"

var (
	hostList    []string
	servers     serverList
	slaves      serverList
	master      *ServerMonitor
	exit        bool
	vy          int
	dbUser      string
	dbPass      string
	rplUser     string
	rplPass     string
	failCount   int
	failoverCtr int
	failoverTs  int64
	tlog        TermLog
	ignoreList  []string
	logPtr      *os.File
	exitMsg     string
	termlength  int
)

const (
	stateFailed string = "Failed"
	stateMaster string = "Master"
	stateSlave  string = "Slave"
	stateUnconn string = "Unconnected"
)

var (
	conf        string
	version     bool
	user        string
	hosts       string
	socket      string
	rpluser     string
	interactive bool
	verbose     bool
	preScript   string
	postScript  string
	maxDelay    int64
	gtidCheck   bool
	prefMaster  string
	ignoreSrv   string
	waitKill    int64
	readonly    bool
	maxfail     int
	autorejoin  bool
	logfile     string
	timeout     int
	faillimit   int
	failtime    int64
)

func init() {
	var errLog = mysql.Logger(log.New(ioutil.Discard, "", 0))
	mysql.SetLogger(errLog)
	rootCmd.AddCommand(switchoverCmd)
	rootCmd.AddCommand(failoverCmd)
	rootCmd.AddCommand(monitorCmd)
	initRepmgrFlags(switchoverCmd)
	initRepmgrFlags(failoverCmd)
	initRepmgrFlags(monitorCmd)
}

func initRepmgrFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&preScript, "pre-failover-script", "", "Path of pre-failover script")
	cmd.Flags().StringVar(&postScript, "post-failover-script", "", "Path of post-failover script")
	cmd.Flags().Int64Var(&maxDelay, "maxdelay", 0, "Maximum replication delay before initiating failover")
	cmd.Flags().BoolVar(&gtidCheck, "gtidcheck", false, "Check that GTID sequence numbers are identical before initiating failover")
	cmd.Flags().StringVar(&prefMaster, "prefmaster", "", "Preferred candidate server for master failover, in host:[port] format")
	cmd.Flags().StringVar(&ignoreSrv, "ignore-servers", "", "List of servers to ignore in slave promotion operations")
	cmd.Flags().Int64Var(&waitKill, "wait-kill", 5000, "Wait this many milliseconds before killing threads on demoted master")
	cmd.Flags().BoolVar(&readonly, "readonly", true, "Set slaves as read-only after switchover")
	cmd.Flags().IntVar(&maxfail, "failcount", 5, "Trigger failover after N failures (interval 1s)")
	cmd.Flags().BoolVar(&autorejoin, "autorejoin", true, "Automatically rejoin a failed server to the current master.")
	cmd.Flags().StringVar(&logfile, "logfile", "", "Write MRM messages to a log file")
	cmd.Flags().IntVar(&timeout, "connect-timeout", 5, "Database connection timeout in seconds")
	cmd.Flags().IntVar(&faillimit, "failover-limit", 0, "In auto-monitor mode, quit after N failovers (0: unlimited)")
	cmd.Flags().Int64Var(&failtime, "failover-time-limit", 0, "In auto-monitor mode, wait N seconds before attempting next failover (0: do not wait)")
}

var failoverCmd = &cobra.Command{
	Use:   "failover",
	Short: "Failover a dead master",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {
		repmgrFlagCheck()
		err := topologyInit()
		if err != nil {
			log.Fatalln(err)
		}
		masterFailover(true)
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		for _, server := range servers {
			defer server.Conn.Close()
		}
	},
}

var switchoverCmd = &cobra.Command{
	Use:   "switchover",
	Short: "Perform a master switch",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {
		repmgrFlagCheck()
		err := topologyInit()
		if err != nil {
			log.Fatalln(err)
		}
		masterFailover(false)
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		for _, server := range servers {
			defer server.Conn.Close()
		}
	},
}

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Start the interactive replication monitor",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {

		repmgrFlagCheck()

		err := topologyInit()
		if err != nil {
			log.Fatalln(err)
		}

		err = termbox.Init()
		if err != nil {
			log.Fatalln("Termbox initialization error", err)
		}
		_, termlength = termbox.Size()
		loglen := termlength - 9 - (len(hostList) * 3)
		tlog = NewTermLog(loglen)
		if interactive {
			tlog.Add("Monitor started in interactive mode")
		} else {
			tlog.Add("Monitor started in automatic mode")
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
						if master.State == stateFailed {
							masterFailover(true)
						} else {
							logprint("ERROR: Master not failed, cannot initiate failover")
						}
					}
					if event.Key == termbox.KeyCtrlD {
						printTopology()
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
			if master.State == stateFailed && interactive == false {
				rem := (failoverTs + failtime) - time.Now().Unix()
				if (failtime == 0) || (failtime > 0 && (rem <= 0 || failoverCtr == 0)) {
					masterFailover(true)
					if failoverCtr == faillimit {
						exitMsg = "INFO : Failover limit reached. Exiting on failover completion."
						exit = true
					}
				} else if failtime > 0 && rem%10 == 0 {
					logprintf("WARN : Failover time limit enforced. Next failover available in %d seconds.", rem)
				}
			}
		}
		termbox.Close()
		if exitMsg != "" {
			log.Println(exitMsg)
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		for _, server := range servers {
			defer server.Conn.Close()
		}
	},
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

func repmgrFlagCheck() {
	if logfile != "" {
		var err error
		logPtr, err = os.Create(logfile)
		if err != nil {
			log.Println("ERROR: Error opening logfile, disabling for the rest of the session.")
			logfile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if hosts != "" {
		hostList = strings.Split(hosts, ",")
	} else {
		log.Fatal("ERROR: No hosts list specified.")
	}
	// validate users.
	if user == "" {
		log.Fatal("ERROR: No master user/pair specified.")
	}
	dbUser, dbPass = splitPair(user)
	if rpluser == "" {
		log.Fatal("ERROR: No replication user/pair specified.")
	}
	rplUser, rplPass = splitPair(rpluser)

	if ignoreSrv != "" {
		ignoreList = strings.Split(ignoreSrv, ",")
	}

	// Check if preferred master is included in Host List
	ret := func() bool {
		for _, v := range hostList {
			if v == prefMaster {
				return true
			}
		}
		return false
	}
	if ret() == false && prefMaster != "" {
		log.Fatal("ERROR: Preferred master is not included in the hosts option")
	}
}
