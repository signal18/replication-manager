// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Author: Stephane Varoqui <stephane@mariadb.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	termbox "github.com/nsf/termbox-go"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/termlog"
)

var (
	cliUser                   string
	cliPassword               string
	cliHost                   string
	cliPort                   string
	cliCert                   string
	cliNoCheckCert            bool
	cliToken                  string
	cliClusters               []string
	cliClusterIndex           int
	cliTlog                   termlog.TermLog
	cliTermlength             int
	cliServers                []cluster.ServerMonitor
	cliMaster                 cluster.ServerMonitor
	cliSettings               Settings
	cliUrl                    string
	cliRuntests               string
	cliShowtests              bool
	cliTeststopcluster        bool
	cliTeststartcluster       bool
	cliTestConvert            bool
	cliTestConvertFile        string
	cliTestResultDBCredential string
	cliTestResultDBServer     string
	cliTopology               string
	cliCleanall               bool
	cliExit                   bool
	cliPrefMaster             string
)

type RequetParam struct {
	key   string
	value string
}

var cliConn = http.Client{
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	Timeout:   1200 * time.Second,
}

func cliInit(needcluster bool) {
	var err error
	cliClusters, err = cliGetClusters()
	if err != nil {
		log.WithError(err).Fatal()
		return
	}
	cliToken, err = cliLogin()
	if err != nil {
		log.WithError(err).Fatal()
		return
	}
	allCLusters, _ := cliGetAllClusters()
	if len(cliClusters) != 1 && needcluster {
		err = errors.New("No cluster specify")
		log.WithError(err).Fatal(fmt.Sprintf("No cluster specify use --cluster in values %s", allCLusters))
	}
	cliServers, err = cliGetServers()
	if err != nil {
		log.WithError(err).Fatal()
		return
	}
}
func init() {
	rootCmd.AddCommand(clientCmd)
	rootCmd.AddCommand(switchoverCmd)
	rootCmd.AddCommand(failoverCmd)
	rootCmd.AddCommand(topologyCmd)
	rootCmd.AddCommand(apiCmd)
	rootCmd.AddCommand(testCmd)

	clientCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	clientCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	clientCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	clientCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	clientCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	clientCmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")

	apiCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	apiCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	apiCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	apiCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	apiCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	apiCmd.Flags().StringVar(&cliUrl, "url", "https://127.0.0.1:3000/api/clusters", "Url to rest API")

	apiCmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")

	switchoverCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	switchoverCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	switchoverCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	switchoverCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	switchoverCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	switchoverCmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")
	switchoverCmd.Flags().StringVar(&cliPrefMaster, "db-servers-prefered-master", "", "Database preferred candidate in election,  host:[port] format")

	failoverCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	failoverCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	failoverCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	failoverCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	failoverCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	failoverCmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")

	topologyCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	topologyCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	topologyCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	topologyCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	topologyCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	topologyCmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")

	testCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	testCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	testCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	testCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	testCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	testCmd.Flags().StringVar(&cliRuntests, "run-tests", "", "tests list to be run ")
	testCmd.Flags().StringVar(&cliTestResultDBServer, "result-db-server", "", "MariaDB MySQL host to store result")
	testCmd.Flags().StringVar(&cliTestResultDBCredential, "result-db-credential", "", "MariaDB MySQL user:password to store result")

	testCmd.Flags().BoolVar(&cliShowtests, "show-tests", false, "display tests list")
	testCmd.Flags().BoolVar(&cliTeststartcluster, "test-start-cluster", true, "start the cluster between tests")
	testCmd.Flags().BoolVar(&cliTeststopcluster, "test-stop-cluster", true, "stop the cluster between tests")
	testCmd.Flags().BoolVar(&cliTestConvert, "convert", false, "convert test result to html")

	testCmd.Flags().StringVar(&cliTestConvertFile, "file", "", "test result.json")

	rootCmd.AddCommand(bootstrapCmd)
	bootstrapCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	bootstrapCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	bootstrapCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	bootstrapCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	bootstrapCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	bootstrapCmd.Flags().StringVar(&cliTopology, "topology", "master-slave", "master-slave|master-slave-no-gtid|maxscale-binlog|multi-master|multi-tier-slave|multi-master-ring")
	bootstrapCmd.Flags().BoolVar(&cliCleanall, "clean-all", false, "Reset all slaves and binary logs before bootstrapping")
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap a replication environment",
	Long:  `The bootstrap command is used to create a new replication environment from scratch`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(true)
		if cliCleanall == true {
			urlclean := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/replication/cleanup"
			_, err := cliAPICmd(urlclean, nil)
			if err != nil {
				log.Fatal(err)
			} else {
				log.Println("Replication cleanup done")
			}
		}
		urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/replication/bootstrap/" + cliTopology
		_, err := cliAPICmd(urlpost, nil)
		if err != nil {
			log.Fatal(err)
		} else {
			log.Println("Replication bootsrap done")
		}
		//		slogs, _ := cliGetLogs()
		//	cliPrintLog(slogs)
		cliGetTopology()
	},
}

var failoverCmd = &cobra.Command{
	Use:   "failover",
	Short: "Failover a dead master",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {
		var slogs []string
		cliInit(true)
		cliGetTopology()
		cliClusterCmd("actions/failover", nil)
		slogs, _ = cliGetLogs()
		cliPrintLog(slogs)
		cliServers, _ = cliGetServers()
		cliGetTopology()
	},
	PostRun: func(cmd *cobra.Command, args []string) {

	},
}

var switchoverCmd = &cobra.Command{
	Use:   "switchover",
	Short: "Perform a master switch",
	Long: `Performs an online master switch by promoting a slave to master
and demoting the old master to slave`,
	Run: func(cmd *cobra.Command, args []string) {
		var slogs []string
		var prefMasterParam RequetParam
		var params []RequetParam

		cliInit(true)
		cliGetTopology()
		if cliPrefMaster != "" {
			prefMasterParam.key = "prefmaster"
			prefMasterParam.value = cliPrefMaster
			params = append(params, prefMasterParam)
			cliClusterCmd("actions/switchover", params)
		} else {
			cliClusterCmd("actions/switchover", nil)
		}
		slogs, _ = cliGetLogs()
		cliPrintLog(slogs)
		cliServers, _ = cliGetServers()
		cliGetTopology()

	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.

	},
}

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Call JWT API",
	Long:  `Performs call to jwt api served by monitoring`,
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(false)
		res, err := cliAPICmd(cliUrl, nil)
		if err != nil {
			log.Fatal("Error in API call")
		} else {
			fmt.Printf(res)
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
	},
}

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Print replication topology",
	Long:  `Print the replication topology by detecting master and slaves`,
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)
		cliGetTopology()
	},
	PostRun: func(cmd *cobra.Command, args []string) {

	},
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Perform regression test",
	Long:  `Perform named tests passed with argument --run-tests=test1,test2`,
	Run: func(cmd *cobra.Command, args []string) {

		if cliTestConvert {

			type TestResults struct {
				Results []cluster.Test `json:"results"`
			}
			var cltests TestResults
			file, err := ioutil.ReadFile(cliTestConvertFile)
			if err != nil {
				fmt.Printf("File error: %v\n", err)
				return
			}
			err = json.Unmarshal(file, &cltests)
			if err != nil {
				fmt.Printf("File error: %v\n", err)
				return
			}
			var tmplgreen = "<tr><td>%s</td><td bgcolor=\"#adebad\">%s</td></tr>"
			var tmplred = "<tr><td>%s</td><td  bgcolor=\"##ff8080\">%s</td></tr>"
			fmt.Printf("<table>")
			for _, v := range cltests.Results {
				if v.Result == "FAIL" {
					fmt.Printf(tmplred, v.Name, v.Result)
				} else {
					fmt.Printf(tmplgreen, v.Name, v.Result)
				}
			}
			fmt.Printf("</table>")
			return
		}
		cliInit(true)
		//cliGetTopology()

		if cliShowtests == true {
			cliSettings, _ = cliGetSettings()
			log.Println(cliSettings.RegTests)
		}
		if cliShowtests == false {

			todotests := strings.Split(cliRuntests, ",")

			for _, test := range todotests {
				var thistest cluster.Test
				thistest.Result = "TIMEOUT"
				thistest.Name = test
				data, _ := json.MarshalIndent(thistest, "", "\t")
				urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/tests/actions/run/" + test

				var startcluster RequetParam
				var stopcluster RequetParam
				var params []RequetParam

				startcluster.key = "startcluster"
				startcluster.value = strconv.FormatBool(cliTeststartcluster)
				params = append(params, startcluster)
				stopcluster.key = "stopcluster"
				stopcluster.value = strconv.FormatBool(cliTeststopcluster)
				params = append(params, stopcluster)

				res, err := cliAPICmd(urlpost, params)
				if err != nil {
					fmt.Printf(string(data))
					log.Fatal("Error in API call")
				} else {
					if res != "" {
						fmt.Printf(res)

						err = json.Unmarshal([]byte(res), &thistest)
						if err != nil {
							fmt.Printf("No valid json in test result: %v\n", err)
							return
						}
						// post result in database
						if cliTestResultDBServer != "" {
							params := fmt.Sprintf("?timeout=2s")
							dsn := cliTestResultDBCredential + "@"
							dsn += "tcp(" + cliTestResultDBServer + ")/" + params
							c, err := sqlx.Open("mysql", dsn)
							if err != nil {
								fmt.Printf("Could not connect to result database %s", err)
							}
							err = c.Ping()
							if err != nil {
								fmt.Printf("Could not connect to result database %s", err)
							}
							_, err = c.Query("REPLACE INTO result.tests (version,test,path,result) VALUES('" + FullVersion + "','" + thistest.Name + "','" + thistest.ConfigFile + "','" + thistest.Result + "')")
							if err != nil {
								fmt.Printf("Could play sql to result database %s", err)
							}

							c.Close()
						}

					} else {
						fmt.Printf(string(data))
					}
				}
			}
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
	},
}

var clientCmd = &cobra.Command{
	Use:   "console",
	Short: "Starts the interactive replication-manager console",
	Long:  "Connect to replication-manager in stateful TLS JWT mode.",
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(false)

		err := termbox.Init()
		if err != nil {
			log.WithError(err).Fatal("Termbox initialization error")
		}
		_, cliTermlength = termbox.Size()
		if cliTermlength == 0 {
			cliTermlength = 120
		} else if cliTermlength < 18 {
			log.Fatal("Terminal too small, please increase window size")
		}
		loglen := cliTermlength - 9 - (len(strings.Split(conf.Hosts, ",")) * 3)
		termboxChan := cliNewTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * time.Duration(2))

		for cliExit == false {
			select {
			case <-ticker.C:
				cliSettings, _ = cliGetSettings()
				cliServers, _ = cliGetServers()
				cliMaster, _ = cliGetMaster()
				dlogs, _ := cliGetLogs()
				cliTlog = termlog.NewTermLog(loglen)
				cliAddTlog(dlogs)
				cliDisplay()
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						cliClusterCmd("actions/switchover", nil)
					}
					if event.Key == termbox.KeyCtrlF {
						if cliMaster.State == "Failed" {
							cliClusterCmd("actions/failover", nil)
						}
					}
					if event.Key == termbox.KeyCtrlD {

						//call topology
					}
					if event.Key == termbox.KeyCtrlN {
						cliClusterIndex++
						if cliClusterIndex >= len(cliClusters) {
							cliClusterIndex = 0
						}
					}
					if event.Key == termbox.KeyCtrlP {
						cliClusterIndex--
						if cliClusterIndex < 0 {
							cliClusterIndex = len(cliClusters) - 1
						}
					}
					if event.Key == termbox.KeyCtrlR {
						cliClusterCmd("settings/switch/readonly", nil)
					}
					if event.Key == termbox.KeyCtrlW {
						cliClusterCmd("settings/switch/readonly", nil)
					}
					if event.Key == termbox.KeyCtrlI {
						cliClusterCmd("settings/switch/interactive", nil)
					}
					if event.Key == termbox.KeyCtrlV {
						cliClusterCmd("settings/switch/verbosity", nil)
					}
					if event.Key == termbox.KeyCtrlE {
						cliClusterCmd("settings/reset/failovercontrol", nil)
					}
					if event.Key == termbox.KeyCtrlH {
						cliDisplayHelp()
					}
					if event.Key == termbox.KeyCtrlQ {
						cliExit = true
					}
					if event.Key == termbox.KeyCtrlC {
						cliExit = true
					}
				}
				switch event.Ch {
				case 's':
					termbox.Sync()
				}
			}
		}

	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.

		termbox.Close()
		if memprofile != "" {
			f, err := os.Create(memprofile)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(f)
			f.Close()
		}
		log.Println(cliToken)
	},
}

func cliGetClusters() ([]string, error) {
	var cl []string
	var err error
	if cfgGroup != "" {
		cl = strings.Split(cfgGroup, ",")
	} else {
		cl, err = cliGetAllClusters()
		//		log.Printf("%s", cl)
		if err != nil {
			return cl, err
		}
	}
	return cl, nil
}

func cliNewTbChan() chan termbox.Event {
	termboxChan := make(chan termbox.Event)
	go func() {
		for {
			termboxChan <- termbox.PollEvent()
		}
	}()
	return termboxChan
}

func cliGetTopology() {

	headstr := ""

	if cliClusters[cliClusterIndex] != "" {
		headstr += fmt.Sprintf("| Group: %s", cliClusters[cliClusterIndex])
	}
	if cliSettings.Interactive == "false" {
		headstr += " |  Mode: Automatic "
	} else {
		headstr += " |  Mode: Manual "
	}

	headstr += fmt.Sprintf("\n%15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")

	for _, server := range cliServers {
		var gtidCurr string
		var gtidSlave string
		if server.CurrentGtid != nil {
			gtidCurr = server.CurrentGtid.Sprint()
		} else {
			gtidCurr = ""
		}
		if server.SlaveGtid != nil {
			gtidSlave = server.SlaveGtid.Sprint()
		} else {
			gtidSlave = ""
		}

		headstr += fmt.Sprintf("\n%15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", server.Host, server.Port, server.State, server.FailCount, server.UsingGtid, gtidCurr, gtidSlave, "", server.Delay.Int64, server.ReadOnly)

	}
	log.Printf(headstr)
}

func cliDisplay() {

	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" Replication Manager Client ")

	if cliClusters[cliClusterIndex] != "" {
		headstr += fmt.Sprintf("| Group: %s", cliClusters[cliClusterIndex])
	}
	if cliSettings.Interactive == "false" {
		headstr += " |  Mode: Automatic "
	} else {
		headstr += " |  Mode: Manual "
	}
	cliPrintfTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, headstr)
	cliPrintfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	cliTlog.Line = 3
	for _, server := range cliServers {
		var gtidCurr string
		var gtidSlave string
		if server.CurrentGtid != nil {
			gtidCurr = server.CurrentGtid.Sprint()
		} else {
			gtidCurr = ""
		}
		if server.SlaveGtid != nil {
			gtidSlave = server.SlaveGtid.Sprint()
		} else {
			gtidSlave = ""
		}

		var fgCol termbox.Attribute
		switch server.State {
		case "Master":
			fgCol = termbox.ColorGreen
		case "Failed":
			fgCol = termbox.ColorRed
		case "Unconnected":
			fgCol = termbox.ColorBlue
		case "Suspect":
			fgCol = termbox.ColorMagenta
		case "SlaveErr":
			fgCol = termbox.ColorMagenta
		case "SlaveLate":
			fgCol = termbox.ColorYellow
		default:
			fgCol = termbox.ColorWhite
		}
		cliPrintfTb(0, cliTlog.Line, fgCol, termbox.ColorBlack, "%15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", server.Host, server.Port, server.State, server.FailCount, server.UsingGtid, gtidCurr, gtidSlave, server.ReplicationHealth, server.Delay.Int64, server.ReadOnly)
		cliTlog.Line++
	}
	cliTlog.Line++
	if cliMaster.State != "Failed" {
		cliPrintTb(0, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-S to switchover, Ctrl-(N|P) to change Cluster,Ctrl-H to help")
	} else {
		cliPrintTb(0, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-F to failover, Ctrl-(N|P) to change Cluster,Ctrl-H to help")
	}
	cliTlog.Line = cliTlog.Line + 3
	cliTlog.Print()

	termbox.Flush()
	_, newlen := termbox.Size()
	if newlen == 0 {
		// pass
	} else if newlen > cliTermlength {
		cliTermlength = newlen
		cliTlog.Len = cliTermlength - 9 - (len(cliClusters) * 3)
		cliTlog.Extend()
	} else if newlen < cliTermlength {
		cliTermlength = newlen
		cliTlog.Len = cliTermlength - 9 - (len(cliClusters) * 3)
		cliTlog.Shrink()
	}
}

func cliAddTlog(dlogs []string) {
	cliTlog.Shrink()
	for _, dl := range dlogs {
		cliTlog.Add(dl)
	}
}

func cliDisplayHelp() {
	cliLogPrint("HELP : Ctrl-D  Print debug information")
	cliLogPrint("HELP : Ctrl-F  Failover")
	cliLogPrint("HELP : Ctrl-S  Switchover")
	cliLogPrint("HELP : Ctrl-N  Next Cluster")
	cliLogPrint("HELP : Ctrl-P  Previous Cluster")
	cliLogPrint("HELP : Ctrl-Q  Quit")
	cliLogPrint("HELP : Ctrl-C  Quit")
	cliLogPrint("HELP : Ctrl-I  Switch failover automatic/manual")
	cliLogPrint("HELP : Ctrl-R  Switch slaves read-only/read-write")
	cliLogPrint("HELP : Ctrl-V  Switch verbosity")
	cliLogPrint("HELP : Ctrl-E  Erase failover control")

}

func cliPrintLog(msg []string) {
	for _, c := range msg {
		log.Printf(c)
	}
}

func cliPrintTb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func cliPrintfTb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	cliPrintTb(x, y, fg, bg, s)
}

func cliLogPrint(msg ...interface{}) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))

	if cliTlog.Len > 0 {
		s := fmt.Sprint(stamp, "[", cliClusters[cliClusterIndex], "] ", fmt.Sprint(msg...))
		cliTlog.Add(s)
		cliDisplay()
	}

}

func cliLogin() (string, error) {

	urlpost := "https://" + cliHost + ":" + cliPort + "/api/login"
	var jsonStr = []byte(`{"username":"` + cliUser + `", "password":"` + cliPassword + `"}`)
	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", errors.New("Wrong credentential")
	}

	type Result struct {
		Token string `json:"token"`
	}
	var r Result
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	return r.Token, nil
}

func cliGetAllClusters() ([]string, error) {
	var r Settings
	var res []string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return res, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return res, err
	}

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return res, err
	}
	return r.Clusters, nil
}

func cliGetSettings() (Settings, error) {
	var r Settings
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/settings"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	return r, nil
}

func cliGetServers() ([]cluster.ServerMonitor, error) {
	var r []cluster.ServerMonitor
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/servers"
	//log.Println("INFO ", urlpost)
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)

	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	resp.Body.Close()
	return r, nil
}

func cliGetMaster() (cluster.ServerMonitor, error) {
	var r cluster.ServerMonitor
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/master"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	resp.Body.Close()
	return r, nil
}

func cliGetLogs() ([]string, error) {
	var r []string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/logs"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	resp.Body.Close()
	return r, nil
}

func cliClusterCmd(command string, params []RequetParam) error {
	//var r string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/" + command
	var bearer = "Bearer " + cliToken

	data := url.Values{}
	data.Add("customer_name", "value")
	if params != nil {
		for _, param := range params {
			data.Add(param.key, param.value)
		}
	}
	b := bytes.NewBuffer([]byte(data.Encode()))

	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return err
	}
	cliTlog.Add(string(body))
	/*err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return err
	}*/
	return nil
}

func cliAPICmd(urlpost string, params []RequetParam) (string, error) {
	//var r string
	var bearer = "Bearer " + cliToken
	var err error
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return "", err
	}
	//	ctx, _ := context.WithTimeout(context.Background(), 600*time.Second)
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", errors.New(string(body))
	}
	return string(body), nil
}
