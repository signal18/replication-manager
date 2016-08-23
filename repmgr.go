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
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/nsf/termbox-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/state"
	"github.com/tanji/replication-manager/termlog"
)

const repmgrVersion string = "0.7-dev"

// Global variables
var (
	hostList    []string
	servers     serverList
	slaves      serverList
	master      *ServerMonitor
	exit        bool
	dbUser      string
	dbPass      string
	rplUser     string
	rplPass     string
	failCount   int
	failoverCtr int
	failoverTs  int64
	tlog        termlog.TermLog
	ignoreList  []string
	logPtr      *os.File
	exitMsg     string
	termlength  int
	sme         *state.StateMachine
	swChan      = make(chan bool)
)

// Configuration variables - do not put global variables in that list
var (
	conf               string
	version            bool
	user               string
	hosts              string
	socket             string
	rpluser            string
	interactive        bool
	verbose            bool
	preScript          string
	postScript         string
	maxDelay           int64
	gtidCheck          bool
	prefMaster         string
	ignoreSrv          string
	waitKill           int64
	readonly           bool
	maxfail            int
	autorejoin         bool
	logfile            string
	timeout            int
	faillimit          int
	failtime           int64
	checktype          string
	masterConn         string
	multiMaster        bool
	bindaddr           string
	httpport           string
	httpserv           bool
	httproot           string
	daemon             bool
	mailFrom           string
	mailTo             string
	mailSMTPAddr       string
	masterConnectRetry int
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
	monitorCmd.Flags().IntVar(&maxfail, "failcount", 5, "Trigger failover after N failures (interval 1s)")
	monitorCmd.Flags().BoolVar(&autorejoin, "autorejoin", true, "Automatically rejoin a failed server to the current master")
	monitorCmd.Flags().StringVar(&checktype, "check-type", "tcp", "Type of server health check (tcp, agent)")
	monitorCmd.Flags().BoolVar(&httpserv, "http-server", false, "Start the HTTP monitor")
	monitorCmd.Flags().StringVar(&bindaddr, "http-bind-address", "localhost", "Bind HTTP monitor to this IP address")
	monitorCmd.Flags().StringVar(&httpport, "http-port", "10001", "HTTP monitor to listen on this port")
	monitorCmd.Flags().StringVar(&httproot, "http-root", "/usr/share/replication-manager/dashboard", "Path to HTTP monitor files")
	monitorCmd.Flags().StringVar(&mailFrom, "mail-from", "mrm@localhost", "Alert email sender")
	monitorCmd.Flags().StringVar(&mailTo, "mail-to", "", "Alert email recipients, separated by commas")
	monitorCmd.Flags().StringVar(&mailSMTPAddr, "mail-smtp-addr", "localhost:25", "Alert email SMTP server address, in host:[port] format")
	monitorCmd.Flags().BoolVar(&daemon, "daemon", false, "Daemon mode. Do not start the Termbox console")
	monitorCmd.Flags().BoolVar(&interactive, "interactive", true, "Ask for user interaction when failures are detected")
	viper.BindPFlags(monitorCmd.Flags())
	maxfail = viper.GetInt("failcount")
	autorejoin = viper.GetBool("autorejoin")
	checktype = viper.GetString("check-type")
	httpserv = viper.GetBool("http-server")
	bindaddr = viper.GetString("http-bind-address")
	httpport = viper.GetString("http-port")
	httproot = viper.GetString("http-root")
	mailTo = viper.GetString("mail-to")
	mailFrom = viper.GetString("mail-from")
	mailSMTPAddr = viper.GetString("mail-smtp-addr")
	daemon = viper.GetBool("daemon")
	interactive = viper.GetBool("interactive")
}

func initRepmgrFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&preScript, "pre-failover-script", "", "Path of pre-failover script")
	cmd.Flags().StringVar(&postScript, "post-failover-script", "", "Path of post-failover script")
	cmd.Flags().Int64Var(&maxDelay, "maxdelay", 0, "Maximum replication delay before initiating failover")
	cmd.Flags().BoolVar(&gtidCheck, "gtidcheck", false, "Do not initiate failover unless slaves are fully in sync")
	cmd.Flags().StringVar(&prefMaster, "prefmaster", "", "Preferred candidate server for master failover, in host:[port] format")
	cmd.Flags().StringVar(&ignoreSrv, "ignore-servers", "", "List of servers to ignore in slave promotion operations")
	cmd.Flags().Int64Var(&waitKill, "wait-kill", 5000, "Wait this many milliseconds before killing threads on demoted master")
	cmd.Flags().BoolVar(&readonly, "readonly", true, "Set slaves as read-only after switchover")
	cmd.Flags().StringVar(&logfile, "logfile", "", "Write MRM messages to a log file")
	cmd.Flags().IntVar(&timeout, "connect-timeout", 5, "Database connection timeout in seconds")
	cmd.Flags().StringVar(&masterConn, "master-connection", "", "Connection name to use for multisource replication")
	cmd.Flags().BoolVar(&multiMaster, "multimaster", false, "Turn on multi-master detection")
	viper.BindPFlags(cmd.Flags())
	cmd.Flags().IntVar(&faillimit, "failover-limit", 0, "Quit monitor after N failovers (0: unlimited)")
	cmd.Flags().Int64Var(&failtime, "failover-time-limit", 0, "In automatic mode, Wait N seconds before attempting next failover (0: do not wait)")
	cmd.Flags().IntVar(&masterConnectRetry, "master-connect-retry", 10, "Specifies how many seconds to wait between slave connect retries to master")
	preScript = viper.GetString("pre-failover-script")
	postScript = viper.GetString("post-failover-script")
	maxDelay = int64(viper.GetInt("maxdelay"))
	gtidCheck = viper.GetBool("gtidcheck")
	prefMaster = viper.GetString("prefmaster")
	ignoreSrv = viper.GetString("ignore-servers")
	waitKill = int64(viper.GetInt("wait-kill"))
	readonly = viper.GetBool("readonly")
	logfile = viper.GetString("logfile")
	timeout = viper.GetInt("connect-timeout")
	masterConn = viper.GetString("master-connection")
	multiMaster = viper.GetBool("multimaster")
	faillimit = viper.GetInt("failover-limit")
	failtime = int64(viper.GetInt("failover-time-limit"))
	masterConnectRetry = viper.GetInt("master-connect-retry")
}

var failoverCmd = &cobra.Command{
	Use:   "failover",
	Short: "Failover a dead master",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {
		repmgrFlagCheck()
		sme = new(state.StateMachine)
		sme.Init()
		// Recover state from file before doing anything else
		sf := stateFile{Name: "/tmp/mrm.state"}
		err := sf.access()
		if err != nil {
			logprint("WARN : Could not create state file")
		}
		err = sf.read()
		if err != nil {
			logprint("WARN : Could not read values from state file:", err)
		} else {
			failoverCtr = int(sf.Count)
			failoverTs = sf.Timestamp
		}
		newServerList()
		err = topologyDiscover()
		if err != nil {
			for _, s := range sme.GetState() {
				log.Println(s)
			}
			// Test for ERR00012 - No master detected
			if sme.CurState.Search("ERR00012") {
				for _, s := range servers {
					if s.State == "" {
						s.State = stateFailed
						master = s
					}
				}
			} else {
				log.Fatalln(err)
			}
		}
		if master == nil {
			log.Fatalln("ERROR: Could not find a failed server in the hosts list")
		}
		if faillimit > 0 && failoverCtr >= faillimit {
			log.Fatalf("ERROR: Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", faillimit)
		}
		rem := (failoverTs + failtime) - time.Now().Unix()
		if failtime > 0 && rem > 0 {
			log.Fatalf("ERROR: Failover time limit enforced. Next failover available in %d seconds", rem)
		}
		if masterFailover(true) {
			sf.Count++
			sf.Timestamp = failoverTs
			err := sf.write()
			if err != nil {
				logprint("WARN : Could not write values to state file:", err)
			}
		}
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
	Long: `Performs an online master switch by promoting a slave to master
and demoting the old master to slave`,
	Run: func(cmd *cobra.Command, args []string) {
		repmgrFlagCheck()
		sme = new(state.StateMachine)
		sme.Init()
		newServerList()
		err := topologyDiscover()
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
	Long: `Starts replication-manager in stateful monitor daemon mode.
Interactive console and HTTP dashboards are available for control`,
	Run: func(cmd *cobra.Command, args []string) {

		repmgrFlagCheck()

		if httpserv {
			go httpserver()
		}

		if !daemon {
			err := termbox.Init()
			if err != nil {
				log.Fatalln("Termbox initialization error", err)
			}
		}

		// Initialize the state machine at this stage where everything is fine.
		sme = new(state.StateMachine)
		sme.Init()

		if daemon {
			termlength = 40
		} else {
			_, termlength = termbox.Size()
			if termlength == 0 {
				termlength = 120
			}
		}
		loglen := termlength - 9 - (len(hostList) * 3)
		tlog = termlog.NewTermLog(loglen)
		if interactive {
			tlog.Add("INFO : Monitor started in manual mode")
		} else {
			tlog.Add("INFO : Monitor started in automatic mode")
		}

		newServerList()

		termboxChan := newTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * 2)
		for exit == false {

			select {
			case <-ticker.C:
				if sme.IsDiscovered() == false {
					if loglevel > 2 {
						logprint("DEBUG: Discovering topology loop")
					}
					pingServerList()
					topologyDiscover()
					states := sme.GetState()
					for i := range states {
						logprint(states[i])
					}
				}
				display()
				if sme.CanMonitor() {
					if loglevel > 2 {
						logprint("DEBUG: Monitoring server loop")
						for k, v := range servers {
							logprintf("DEBUG: Server [%d]: %v", k, v)
						}
						logprintf("DEBUG: Master: %v", master)
						for k, v := range slaves {
							logprintf("DEBUG: Slave [%d]: %v", k, v)
						}
					}
					wg := new(sync.WaitGroup)
					for _, server := range servers {
						wg.Add(1)
						go server.check(wg)
					}
					wg.Wait()
					topologyDiscover()
					states := sme.GetState()
					for i := range states {
						logprint(states[i])
					}
					checkfailed()
					select {
					case sig := <-swChan:
						logprint("INFO: Receiving switchover message from channel")
						if sig {
							masterFailover(false)
						}
					default:
						//do nothing
					}
				}
				sme.ClearState()

			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						if master.State != stateFailed || master.FailCount > 0 {
							masterFailover(false)
						} else {
							logprint("ERROR: Master failed, cannot initiate switchover")
						}
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
					if event.Key == termbox.KeyCtrlI {
						toggleInteractive()
					}
					if event.Key == termbox.KeyCtrlH {
						displayHelp()
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
		if exitMsg != "" {
			log.Println(exitMsg)
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		termbox.Close()
		for _, server := range servers {
			defer server.Conn.Close()
		}
	},
}

func checkfailed() {
	// Don't trigger a failover if a switchover is happening
	if master != nil {
		if master.State == stateFailed && interactive == false && master.FailCount >= maxfail {
			rem := (failoverTs + failtime) - time.Now().Unix()
			if (failtime == 0) || (failtime > 0 && (rem <= 0 || failoverCtr == 0)) {
				masterFailover(true)
				if failoverCtr == faillimit {
					sme.AddState("INF00002", state.State{ErrType: "INFO", ErrDesc: "Failover limit reached. Switching to manual mode", ErrFrom: "MON"})
					interactive = true
				}
			} else if failtime > 0 && rem%10 == 0 {
				logprintf("WARN : Failover time limit enforced. Next failover available in %d seconds", rem)
			} else {
				logprintf("WARN : Constraint is blocking for failover")
			}

		} else {
			//	logprintf("WARN : Constraint is blocking master state %s stateFailed %s interactive %b master.FailCount %d >= maxfail %d" ,master.State,stateFailed,interactive, master.FailCount , maxfail )
		}
	} else {
		logprintf("WARN : Unknown master when checking failover")
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

// Check that mandatory flags have correct values. This is not part of the state machine and mandatory flags
// must lead to Fatal errors if initialized with wrong values.

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
	pfa := strings.Split(prefMaster, ",")
	if len(pfa) > 1 {
		log.Fatal("ERROR: prefmaster option takes exactly one argument")
	}
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

func toggleInteractive() {
	if interactive == true {
		interactive = false
		logprintf("INFO : Failover monitor switched to automatic mode")
	} else {
		interactive = true
		logprintf("INFO : Failover monitor switched to manual mode")
	}
}
