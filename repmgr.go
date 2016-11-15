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
	"github.com/satori/go.uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tanji/replication-manager/crypto"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"
	"github.com/tanji/replication-manager/termlog"
)

// Global variables
var (
	hostList             []string
	servers              serverList
	slaves               serverList
	master               *ServerMonitor
	exit                 bool
	dbUser               string
	dbPass               string
	rplUser              string
	rplPass              string
	failoverCtr          int
	failoverTs           int64
	tlog                 termlog.TermLog
	ignoreList           []string
	logPtr               *os.File
	exitMsg              string
	termlength           int
	sme                  *state.StateMachine
	swChan               = make(chan bool)
	repmgrHostname       string
	runUUID              string
	runStatus            string
	runOnceAfterTopology bool
)

func init() {
	runUUID = uuid.NewV4().String()
	runStatus = "A"
	runOnceAfterTopology = true
	var errLog = mysql.Logger(log.New(ioutil.Discard, "", 0))
	mysql.SetLogger(errLog)
	rootCmd.AddCommand(switchoverCmd)
	rootCmd.AddCommand(failoverCmd)
	rootCmd.AddCommand(monitorCmd)
	initRepmgrFlags(switchoverCmd)
	initRepmgrFlags(failoverCmd)
	initRepmgrFlags(monitorCmd)
	monitorCmd.Flags().IntVar(&conf.MaxFail, "failcount", 5, "Trigger failover after N failures (interval 1s)")
	monitorCmd.Flags().BoolVar(&conf.Autorejoin, "autorejoin", true, "Automatically rejoin a failed server to the current master")
	monitorCmd.Flags().StringVar(&conf.CheckType, "check-type", "tcp", "Type of server health check (tcp, agent)")
	monitorCmd.Flags().BoolVar(&conf.HttpServ, "http-server", false, "Start the HTTP monitor")
	monitorCmd.Flags().StringVar(&conf.BindAddr, "http-bind-address", "localhost", "Bind HTTP monitor to this IP address")
	monitorCmd.Flags().StringVar(&conf.HttpPort, "http-port", "10001", "HTTP monitor to listen on this port")
	monitorCmd.Flags().StringVar(&conf.HttpRoot, "http-root", "/usr/share/replication-manager/dashboard", "Path to HTTP monitor files")
	monitorCmd.Flags().StringVar(&conf.MailFrom, "mail-from", "mrm@localhost", "Alert email sender")
	monitorCmd.Flags().StringVar(&conf.MailTo, "mail-to", "", "Alert email recipients, separated by commas")
	monitorCmd.Flags().StringVar(&conf.MailSMTPAddr, "mail-smtp-addr", "localhost:25", "Alert email SMTP server address, in host:[port] format")
	monitorCmd.Flags().BoolVar(&conf.Daemon, "daemon", false, "Daemon mode. Do not start the Termbox console")
	monitorCmd.Flags().BoolVar(&conf.Interactive, "interactive", true, "Ask for user interaction when failures are detected")
	monitorCmd.Flags().BoolVar(&conf.RplChecks, "rplchecks", true, "Ignore replication checks for failover purposes")
	monitorCmd.Flags().BoolVar(&conf.MxsOn, "maxscale", false, "Synchronize replication status with MaxScale proxy server")
	monitorCmd.Flags().StringVar(&conf.MxsHost, "maxscale-host", "127.0.0.1", "MaxScale host IP")
	monitorCmd.Flags().StringVar(&conf.MxsPort, "maxscale-port", "6603", "MaxScale admin port")
	monitorCmd.Flags().StringVar(&conf.MxsUser, "maxscale-user", "admin", "MaxScale admin user")
	monitorCmd.Flags().StringVar(&conf.MxsPass, "maxscale-pass", "mariadb", "MaxScale admin password")
	monitorCmd.Flags().BoolVar(&conf.HaproxyOn, "haproxy", false, "Wrapper running haproxy on same host")
	monitorCmd.Flags().IntVar(&conf.HaproxyWritePort, "haproxy-write-port", 3306, "haproxy read-write port to leader")
	monitorCmd.Flags().IntVar(&conf.HaproxyReadPort, "haproxy-read-port", 3307, "haproxy load balance read port to all nodes")
	monitorCmd.Flags().StringVar(&conf.HaproxyBinaryPath, "haproxy-binary-path", "/usr/sbin/haproxy", "MaxScale admin user")

	viper.BindPFlags(monitorCmd.Flags())

	var err error
	repmgrHostname, err = os.Hostname()
	if err != nil {
		log.Fatalln("ERROR: replication-manager could not get hostname from system")
	}
}

func initRepmgrFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&conf.PreScript, "pre-failover-script", "", "Path of pre-failover script")
	cmd.Flags().StringVar(&conf.PostScript, "post-failover-script", "", "Path of post-failover script")
	cmd.Flags().Int64Var(&conf.MaxDelay, "maxdelay", 0, "Maximum replication delay before initiating failover")
	cmd.Flags().BoolVar(&conf.GtidCheck, "gtidcheck", false, "Do not initiate failover unless slaves are fully in sync")
	cmd.Flags().StringVar(&conf.PrefMaster, "prefmaster", "", "Preferred candidate server for master failover, in host:[port] format")
	cmd.Flags().StringVar(&conf.IgnoreSrv, "ignore-servers", "", "List of servers to ignore in slave promotion operations")
	cmd.Flags().Int64Var(&conf.WaitKill, "wait-kill", 5000, "Wait this many milliseconds before killing threads on demoted master")
	cmd.Flags().Int64Var(&conf.WaitTrx, "wait-trx", 10, "Wait this many seconds before transactions end to cancel switchover")
	cmd.Flags().BoolVar(&conf.ReadOnly, "readonly", true, "Set slaves as read-only after switchover")
	cmd.Flags().StringVar(&conf.LogFile, "logfile", "", "Write MRM messages to a log file")
	cmd.Flags().IntVar(&conf.Timeout, "connect-timeout", 5, "Database connection timeout in seconds")
	cmd.Flags().StringVar(&conf.MasterConn, "master-connection", "", "Connection name to use for multisource replication")
	cmd.Flags().BoolVar(&conf.MultiMaster, "multimaster", false, "Turn on multi-master detection")
	cmd.Flags().BoolVar(&conf.Spider, "spider", false, "Turn on spider detection")
	cmd.Flags().BoolVar(&conf.Test, "test", false, "Enable non regression tests ")

	viper.BindPFlags(cmd.Flags())
	cmd.Flags().IntVar(&conf.FailLimit, "failover-limit", 0, "Quit monitor after N failovers (0: unlimited)")
	cmd.Flags().Int64Var(&conf.FailTime, "failover-time-limit", 0, "In automatic mode, Wait N seconds before attempting next failover (0: do not wait)")
	cmd.Flags().IntVar(&conf.MasterConnectRetry, "master-connect-retry", 10, "Specifies how many seconds to wait between slave connect retries to master")
	cmd.Flags().BoolVar(&conf.FailSync, "failover-at-sync", false, "Only failover when state semisync is sync for last status")
	cmd.Flags().BoolVar(&conf.Heartbeat, "heartbeat-table", false, "Heartbeat for active/passive or multi mrm setup")
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
						if conf.LogLevel > 2 {
							logprint("DEBUG: State failed set by state detection ERR00012")
						}
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
		if conf.FailLimit > 0 && failoverCtr >= conf.FailLimit {
			log.Fatalf("ERROR: Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", conf.FailLimit)
		}
		rem := (failoverTs + conf.FailTime) - time.Now().Unix()
		if conf.FailTime > 0 && rem > 0 {
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
	Short: "Start the conf.Interactive replication monitor",
	Long: `Starts replication-manager in stateful monitor daemon mode.
Interactive console and HTTP dashboards are available for control`,
	Run: func(cmd *cobra.Command, args []string) {

		if conf.LogLevel >= 2 {
			log.Printf("%+v", conf)
		}

		repmgrFlagCheck()

		if conf.HttpServ {
			go httpserver()
		}

		if !conf.Daemon {
			err := termbox.Init()
			if err != nil {
				log.Fatalln("Termbox initialization error", err)
			}
		}

		// Initialize the state machine at this stage where everything is fine.
		sme = new(state.StateMachine)
		sme.Init()

		if conf.Daemon {
			termlength = 40
			logprintf("INFO : replication-manager version %s started in daemon mode", repmgrVersion)
		} else {
			_, termlength = termbox.Size()
			if termlength == 0 {
				termlength = 120
			}
		}
		loglen := termlength - 9 - (len(hostList) * 3)
		tlog = termlog.NewTermLog(loglen)
		if conf.Interactive {
			logprint("INFO : Monitor started in manual mode")
		} else {
			logprint("INFO : Monitor started in automatic mode")
		}

		newServerList()

		termboxChan := newTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * 2)
		for exit == false {

			select {
			case <-ticker.C:
				if sme.IsDiscovered() == false {
					if conf.LogLevel > 2 {
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
					/* run once */
					if runOnceAfterTopology {
						if master != nil {
							if conf.HaproxyOn {
								initHaproxy()
							}
							runOnceAfterTopology = false
						}
					}

					if conf.LogLevel > 2 {
						logprint("DEBUG: Monitoring server loop")
						for k, v := range servers {
							logprintf("DEBUG: Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
						}
						logprintf("DEBUG: Master [ ]: URL: %-15s State: %6s PrevState: %6s", master.URL, master.State, master.PrevState)
						for k, v := range slaves {
							logprintf("DEBUG: Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
						}
					}
					wg := new(sync.WaitGroup)
					for _, server := range servers {
						wg.Add(1)
						go server.check(wg)
					}
					wg.Wait()
					pingServerList()
					topologyDiscover()
					states := sme.GetState()
					for i := range states {
						logprint(states[i])
					}
					checkfailed()
					select {
					case sig := <-swChan:
						if sig {
							masterFailover(false)
						}
					default:
						//do nothing
					}
				}
				if !sme.IsInFailover() {
					sme.ClearState()
				}
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
						logprint("INFO : Quitting monitor")
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
	if sme.IsInFailover() {
		logprintf("DEBUG: In Failover skip checking failed master")
		return
	}
	//  logprintf("WARN : Constraint is blocking master state %s stateFailed %s conf.Interactive %b master.FailCount %d >= maxfail %d" ,master.State,stateFailed,interactive, master.FailCount , maxfail )
	if master != nil {
		if master.State == stateFailed && conf.Interactive == false && master.FailCount >= conf.MaxFail {
			rem := (failoverTs + conf.FailTime) - time.Now().Unix()
			if (conf.FailTime == 0) || (conf.FailTime > 0 && (rem <= 0 || failoverCtr == 0)) {
				if failoverCtr == conf.FailLimit {
					sme.AddState("INF00002", state.State{ErrType: "INFO", ErrDesc: "Failover limit reached. Switching to manual mode", ErrFrom: "MON"})
					conf.Interactive = true
				}
				masterFailover(true)
			} else if conf.FailTime > 0 && rem%10 == 0 {
				logprintf("WARN : Failover time limit enforced. Next failover available in %d seconds", rem)
			} else {
				logprintf("WARN : Constraint is blocking for failover")
			}

		} else if master.State == stateFailed && master.FailCount < conf.MaxFail {
			logprintf("WARN : Waiting more prove of master death")

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
	if conf.LogFile != "" {
		var err error
		logPtr, err = os.OpenFile(conf.LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			log.Println("ERROR: Error opening logfile, disabling for the rest of the session.")
			conf.LogFile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if conf.Hosts != "" {
		hostList = strings.Split(conf.Hosts, ",")
	} else {
		log.Fatal("ERROR: No hosts list specified.")
	}
	// validate users
	if conf.User == "" {
		log.Fatal("ERROR: No master user/pair specified.")
	}
	dbUser, dbPass = misc.SplitPair(conf.User)

	if conf.RplUser == "" {
		log.Fatal("ERROR: No replication user/pair specified.")
	}
	rplUser, rplPass = misc.SplitPair(conf.RplUser)

	// If there's an existing encryption key, decrypt the passwords
	k, err := readKey()
	if err != nil {
		log.Println("INFO : No existing password encryption scheme:", err)
	} else {
		p := crypto.Password{Key: k}
		p.CipherText = dbPass
		p.Decrypt()
		dbPass = p.PlainText
		p.CipherText = rplPass
		p.Decrypt()
		rplPass = p.PlainText
	}

	if conf.IgnoreSrv != "" {
		ignoreList = strings.Split(conf.IgnoreSrv, ",")
	}

	// Check if preferred master is included in Host List
	pfa := strings.Split(conf.PrefMaster, ",")
	if len(pfa) > 1 {
		log.Fatal("ERROR: prefmaster option takes exactly one argument")
	}
	ret := func() bool {
		for _, v := range hostList {
			if v == conf.PrefMaster {
				return true
			}
		}
		return false
	}
	if ret() == false && conf.PrefMaster != "" {
		log.Fatal("ERROR: Preferred master is not included in the hosts option")
	}
}

func toggleInteractive() {
	if conf.Interactive == true {
		conf.Interactive = false
		logprintf("INFO : Failover monitor switched to automatic mode")
	} else {
		conf.Interactive = true
		logprintf("INFO : Failover monitor switched to manual mode")
	}
}

func getActiveStatus() {
	for _, sv := range servers {
		err := dbhelper.SetStatusActiveHeartbeat(sv.Conn, runUUID, "A")
		if err == nil {
			runStatus = "A"
		}
	}
}
