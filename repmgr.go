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
	"github.com/spf13/viper"
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
	checktype   string
	masterConn  string
	multiMaster bool
	bindaddr    string
	httpport    string
	httpserv    bool
	daemon      bool
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
	monitorCmd.Flags().IntVar(&faillimit, "failover-limit", 0, "Quit monitor after N failovers (0: unlimited)")
	monitorCmd.Flags().Int64Var(&failtime, "failover-time-limit", 0, "in automatic mode, Wait N seconds before attempting next failover (0: do not wait)")
	monitorCmd.Flags().StringVar(&checktype, "check-type", "tcp", "Type of server health check (tcp, agent)")
	monitorCmd.Flags().BoolVar(&httpserv, "http-server", false, "Start the HTTP monitor")
	monitorCmd.Flags().StringVar(&bindaddr, "http-bind-address", "localhost", "Bind HTTP monitor to this IP address")
	monitorCmd.Flags().StringVar(&httpport, "http-port", "10001", "HTTP monitor to listen on this port")
	monitorCmd.Flags().BoolVar(&daemon, "daemon", false, "Daemon mode. Do not start the Termbox console")
	viper.BindPFlags(monitorCmd.Flags())
	maxfail = viper.GetInt("failcount")
	autorejoin = viper.GetBool("autorejoin")
	faillimit = viper.GetInt("failover-limit")
	failtime = int64(viper.GetInt("failover-time-limit"))
	checktype = viper.GetString("check-type")
	httpserv = viper.GetBool("http-server")
	bindaddr = viper.GetString("http-bind-address")
	httpport = viper.GetString("http-port")
	daemon = viper.GetBool("daemon")
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
	cmd.Flags().StringVar(&logfile, "logfile", "", "Write MRM messages to a log file")
	cmd.Flags().IntVar(&timeout, "connect-timeout", 5, "Database connection timeout in seconds")
	cmd.Flags().StringVar(&masterConn, "master-connection", "", "Connection name to use for multisource replication")
	cmd.Flags().BoolVar(&multiMaster, "multimaster", false, "Turn on multi-master detection")
	viper.BindPFlags(cmd.Flags())
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
		if masterFailover(true) {
			sf := stateFile{Name: "/tmp/mrm.state"}
			// handle if file already exists
			err := sf.access()
			if err != nil {
				logprint("WARN : Could not create state file")
			} else {
				sf.Count = sf.Count + 1
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

		if httpserv {
			go httpserver()
		}

		if !daemon {
			err = termbox.Init()
			if err != nil {
				log.Fatalln("Termbox initialization error", err)
			}
		}
		if daemon {
			termlength = 40
		} else {
			_, termlength = termbox.Size()
		}
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
				for _, server := range servers {
					server.check()
				}
				display()
				checkfailed()
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						if master.State != stateFailed || failCount > 0 {
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

func checkfailed() {
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

	// Check user privileges on live servers
	for _, sv := range servers {
		if sv.State != stateFailed {
			priv, err := dbhelper.GetPrivileges(sv.Conn, dbUser, sv.Host)
			if err != nil {
				log.Fatalf("ERROR: Error getting privileges for user %s on host %s: %s", dbUser, sv.Host, err)
			}
			if priv.Repl_client_priv == "N" {
				log.Fatalln("ERROR: User must have REPLICATION_CLIENT privilege")
			} else if priv.Repl_slave_priv == "N" {
				log.Fatalln("ERROR: User must have REPLICATION_SLAVE privilege")
			} else if priv.Super_priv == "N" {
				log.Fatalln("ERROR: User must have SUPER privilege")
			}
		}
	}
}
