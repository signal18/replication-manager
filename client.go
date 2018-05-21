// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Author: Stephane Varoqui  <svaroqui@gmail.com>
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
	"syscall"

	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	termbox "github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/termlog"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	cliUser                      string
	cliPassword                  string
	cliHost                      string
	cliPort                      string
	cliCert                      string
	cliNoCheckCert               bool
	cliToken                     string
	cliClusters                  []string
	cliClusterIndex              int
	cliTlog                      termlog.TermLog
	cliTermlength                int
	cliServers                   []cluster.ServerMonitor
	cliMaster                    cluster.ServerMonitor
	cliSettings                  Settings
	cliUrl                       string
	cliTTestRun                  string
	cliTestShowTests             bool
	cliTeststopcluster           bool
	cliTeststartcluster          bool
	cliTestConvert               bool
	cliTestConvertFile           string
	cliTestResultDBCredential    string
	cliTestResultDBServer        string
	cliBootstrapTopology         string
	cliBootstrapCleanall         bool
	cliBootstrapWithProvisioning bool
	cliExit                      bool
	cliPrefMaster                string
	cliStatusErrors              bool
	cliServerID                  string
	cliServerMaintenance         bool
	cliServerStop                bool
	cliServerStart               bool
	cliConsoleServerIndex        int
	cliShowObjects               string
	cliConfirm                   string
)

type RequetParam struct {
	key   string
	value string
}

var cliConn = http.Client{
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	Timeout:   1800 * time.Second,
}

func cliGetpasswd() string {
	fmt.Print("Enter Password: ")
	bytePassword, _ := terminal.ReadPassword(int(syscall.Stdin))
	password := string(bytePassword)
	return strings.TrimSpace(password)
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
		cliPassword = cliGetpasswd()

		cliToken, err = cliLogin()
		if err != nil {
			fmt.Printf("\n'%s'\n", err)
			os.Exit(14)
		}
	}
	allCLusters, _ := cliGetAllClusters()
	if len(cliClusters) != 1 && needcluster && cfgGroup == "" {
		err = errors.New("No cluster specify")
		log.WithError(err).Fatal(fmt.Sprintf("No cluster specify use --cluster in values %s", allCLusters))
	}
	if cliClusterInServerList() == false {
		fmt.Println("Cluster not found")
		os.Exit(10)
	}
	cliServers, err = cliGetServers()
	if err != nil {
		log.WithError(err).Fatal()
		return
	}
}

func cliClusterInServerList() bool {
	if cfgGroup == "" {
		return true
	}
	var isValueInList func(value string, list []string) bool
	isValueInList = func(value string, list []string) bool {
		for i, v := range list {
			if v == value {
				cliClusterIndex = i
				return true
			}
		}
		return false
	}

	clinput := strings.Split(cfgGroup, ",")
	for _, ci := range clinput {
		if isValueInList(ci, cliClusters) == false {
			return false
		}
	}

	return true
}
func initCliCommonFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	cmd.Flags().StringVar(&cliPassword, "password", "repman", "Paswword of replication-manager")
	cmd.Flags().StringVar(&cliPort, "port", "10005", "TLS port of  replication-manager")
	cmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	cmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	cmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")
	viper.BindPFlags(cmd.Flags())

}

func init() {

	rootCmd.AddCommand(clientCmd)
	initCliCommonFlags(clientCmd)
	rootCmd.AddCommand(switchoverCmd)
	initCliCommonFlags(switchoverCmd)
	rootCmd.AddCommand(failoverCmd)
	initCliCommonFlags(failoverCmd)
	rootCmd.AddCommand(topologyCmd)
	initCliCommonFlags(topologyCmd)
	rootCmd.AddCommand(apiCmd)
	initCliCommonFlags(apiCmd)
	rootCmd.AddCommand(testCmd)
	initCliCommonFlags(testCmd)
	rootCmd.AddCommand(statusCmd)
	initCliCommonFlags(statusCmd)
	rootCmd.AddCommand(bootstrapCmd)
	initCliCommonFlags(bootstrapCmd)
	rootCmd.AddCommand(serverCmd)
	initCliCommonFlags(serverCmd)
	rootCmd.AddCommand(showCmd)
	initCliCommonFlags(showCmd)

	serverCmd.Flags().StringVar(&cliServerID, "id", "", "server id")
	serverCmd.Flags().BoolVar(&cliServerMaintenance, "maintenance", false, "Toggle maintenance")
	serverCmd.Flags().BoolVar(&cliServerStop, "stop", false, "Start server")
	serverCmd.Flags().BoolVar(&cliServerStart, "start", false, "Stop server")

	apiCmd.Flags().StringVar(&cliUrl, "url", "https://127.0.0.1:10005/api/clusters", "Url to rest API")

	switchoverCmd.Flags().StringVar(&cliPrefMaster, "db-servers-prefered-master", "", "Database preferred candidate in election,  host:[port] format")

	testCmd.Flags().StringVar(&cliTTestRun, "run-tests", "", "tests list to be run ")
	testCmd.Flags().StringVar(&cliTestResultDBServer, "result-db-server", "", "MariaDB MySQL host to store result")
	testCmd.Flags().StringVar(&cliTestResultDBCredential, "result-db-credential", "", "MariaDB MySQL user:password to store result")
	testCmd.Flags().BoolVar(&cliTestShowTests, "show-tests", false, "display tests list")
	testCmd.Flags().BoolVar(&cliTeststartcluster, "test-provision-cluster", true, "start the cluster between tests")
	testCmd.Flags().BoolVar(&cliTeststopcluster, "test-unprovision-cluster", true, "stop the cluster between tests")
	testCmd.Flags().BoolVar(&cliTestConvert, "convert", false, "convert test result to html")

	testCmd.Flags().StringVar(&cliTestConvertFile, "file", "", "test result.json")

	bootstrapCmd.Flags().StringVar(&cliBootstrapTopology, "topology", "master-slave", "master-slave|master-slave-no-gtid|maxscale-binlog|multi-master|multi-tier-slave|multi-master-ring,multi-master-wsrep")
	bootstrapCmd.Flags().BoolVar(&cliBootstrapCleanall, "clean-all", false, "Reset all slaves and binary logs before bootstrapping")
	bootstrapCmd.Flags().BoolVar(&cliBootstrapWithProvisioning, "with-provisioning", false, "Provision the culster for replication-manager-tst or Provision the culster for replication-manager-pro")

	statusCmd.Flags().BoolVar(&cliStatusErrors, "with-errors", false, "Add json errors reporting")

	showCmd.Flags().StringVar(&cliShowObjects, "get", "settings,clusters,servers,master,slaves,crashes,alerts", "get the following objects")

}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run some actions on a server",
	Long:  `The server command is used to stop , start or put a server in maintenace`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(true)
		urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/servers/" + cliServerID + "/actions/maintenance"
		_, err := cliAPICmd(urlpost, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s", err)
			os.Exit(1)
		}
		os.Exit(0)
	},
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap a replication environment",
	Long:  `The bootstrap command is used to create a new replication environment from scratch`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(true)

		if cliBootstrapWithProvisioning == true {
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/services/provision"
			_, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s", err)
				os.Exit(1)
			} else {
				fmt.Println("Provisioning done")
				os.Exit(0)
			}
		} else {

			if cliBootstrapCleanall == true {
				urlclean := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/replication/cleanup"
				_, err := cliAPICmd(urlclean, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s", err)
					os.Exit(1)
				} else {
					fmt.Println("Replication cleanup done")
				}
			}
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/replication/bootstrap/" + cliBootstrapTopology
			_, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s", err)
				os.Exit(2)
			} else {
				fmt.Println("Replication bootsrap done")
			}
			//		slogs, _ := cliGetLogs()
			//	cliPrintLog(slogs)
			cliGetTopology()
		}
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Request status ",
	Long:  `The status command is used to request monitor daemon or pecific cluster status`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(false)
		type Result struct {
			Alive string `json:"alive"`
		}
		var ret Result

		if cfgGroup == "" {
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/status"
			res, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "API call %s", err)
				os.Exit(1)
			} else {
				if res != "" {
					err = json.Unmarshal([]byte(res), &ret)
					if err != nil {
						fmt.Fprintf(os.Stderr, "API call %s", err)
						os.Exit(2)
					} else {
						fmt.Fprintf(os.Stdout, "%s\n", ret.Alive)
						os.Exit(0)
					}
				}
			}
		}
		if cfgGroup != "" {
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/status"
			res, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "API call %s", err)
				os.Exit(1)
			} else {
				if res != "" {
					err = json.Unmarshal([]byte(res), &ret)
					if err != nil {
						fmt.Fprintf(os.Stderr, "API call %s", err)
						os.Exit(2)
					} else {
						if cliStatusErrors {
							urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/alerts"
							res, err := cliAPICmd(urlpost, nil)
							if err != nil {
								fmt.Fprintf(os.Stderr, "API call %s", err)
								os.Exit(3)
							} else {
								fmt.Fprintf(os.Stdout, "%s\n", res)
							}
						} else {
							fmt.Fprintf(os.Stdout, "%s\n", ret.Alive)
						}
						os.Exit(0)
					}
				}
			}
		}
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

		if cliTestShowTests == true {
			cliSettings, _ = cliGetSettings()
			log.Println(cliSettings.RegTests)
		}
		if cliTestShowTests == false {

			todotests := strings.Split(cliTTestRun, ",")

			for _, test := range todotests {
				var thistest cluster.Test
				thistest.Result = "TIMEOUT"
				thistest.Name = test
				data, _ := json.MarshalIndent(thistest, "", "\t")
				urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/tests/actions/run/" + test

				var startcluster RequetParam
				var stopcluster RequetParam
				var params []RequetParam

				startcluster.key = "provision"
				startcluster.value = strconv.FormatBool(cliTeststartcluster)
				params = append(params, startcluster)
				stopcluster.key = "unprovision"
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

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Print json informations",
	Long:  `To use for support issues`,
	Run: func(cmd *cobra.Command, args []string) {
		//cliClusters, err = cliGetClusters()
		cliInit(false)
		urlpost := ""
		type Objects struct {
			Name     string
			Settings Settings                `json:"settings"`
			Servers  []cluster.ServerMonitor `json:"servers"`
			Master   cluster.ServerMonitor   `json:"master"`
			Slaves   []cluster.ServerMonitor `json:"slaves"`
			Crashes  []cluster.Crash         `json:"crashes"`
			Alerts   cluster.Alerts          `json:"alerts"`
		}
		type Report struct {
			Clusters []Objects `json:"clusters"`
		}
		var myReport Report

		for _, cluster := range cliClusters {

			var myObjects Objects
			myObjects.Name = cluster
			if strings.Contains(cliShowObjects, "settings") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {

					json.Unmarshal([]byte(res), &myObjects.Settings)
				}
			}
			if strings.Contains(cliShowObjects, "servers") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/servers"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Servers)
				}
			}
			if strings.Contains(cliShowObjects, "master") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/master"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Master)
				}
			}
			if strings.Contains(cliShowObjects, "slaves") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/master"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Slaves)
				}
			}
			if strings.Contains(cliShowObjects, "crashes") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/crashes"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Crashes)
				}
			}
			if strings.Contains(cliShowObjects, "alerts") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/alerts"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Alerts)
				}
			}
			myReport.Clusters = append(myReport.Clusters, myObjects)

		}
		data, err := json.MarshalIndent(myReport, "", "\t")
		if err != nil {
			fmt.Println(err)
			os.Exit(10)
		}

		fmt.Fprintf(os.Stdout, "%s\n", data)
	},
	PostRun: func(cmd *cobra.Command, args []string) {

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
						//	fmt.Println("Confirm switchover ? [Y,y]")
						cliConfirm = "Confirm switchover ? [Y,y]"

						cliDisplay()
					confirmloop:
						for {
							select {
							case ev := <-termboxChan:
								switch ev.Type {
								case termbox.EventKey:

									if ev.Ch == 89 || ev.Ch == 121 {
										cliClusterCmd("actions/switchover", nil)
									}
									cliConfirm = ""
									break confirmloop

								}
							}
						}
					}
					if event.Key == termbox.KeyCtrlF {
						if cliMaster.State == "Failed" {
							cliClusterCmd("actions/failover", nil)
						}
					}
					if event.Key == termbox.KeyCtrlM {
						cliClusterCmd("servers/"+cliServers[cliConsoleServerIndex].Id+"/actions/maintenance", nil)
					}

					if event.Key == termbox.KeyArrowUp {
						cliConsoleServerIndex--
						if cliConsoleServerIndex < 0 {
							cliConsoleServerIndex = len(cliServers) - 1
						}
					}
					if event.Key == termbox.KeyArrowDown {
						cliConsoleServerIndex++
						if cliConsoleServerIndex >= len(cliServers) {
							cliConsoleServerIndex = 0
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
		//		log.Println(cliToken)
	},
}

func cliGetClusters() ([]string, error) {
	var cl []string
	var err error
	cl, err = cliGetAllClusters()
	if err != nil {
		return cl, err
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

	headstr += fmt.Sprintf("\n%19s %15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", "Id", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")

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

		headstr += fmt.Sprintf("\n%19s %15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", server.Id, server.Host, server.Port, server.State, server.FailCount, server.GetReplicationUsingGtid(), gtidCurr, gtidSlave, "", server.GetReplicationDelay(), server.ReadOnly)

	}
	fmt.Printf(headstr)
	fmt.Printf("\n")
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
	cliPrintfTb(0, 1, termbox.ColorRed, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, cliConfirm)
	cliPrintfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%1s%15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", " ", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	cliTlog.Line = 3
	for i, server := range cliServers {
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
		mystatus := server.State
		if server.IsVirtualMaster {
			mystatus = mystatus + "*VM"
		}
		myServerPointer := " "
		if i == cliConsoleServerIndex {
			myServerPointer = ">"
		}
		cliPrintfTb(1, cliTlog.Line, fgCol, termbox.ColorBlack, "%1s%15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", myServerPointer, server.Host, server.Port, mystatus, server.FailCount, server.GetReplicationUsingGtid(), gtidCurr, gtidSlave, server.ReplicationHealth, server.GetReplicationDelay(), server.ReadOnly)
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
	cliLogPrint("HELP : Ctrl-M  Maintenance")
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
		log.Println("ERROR in login", err)
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
		log.Println("ERROR", err)
		return res, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR", err)
		return res, err
	}

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in cluster list", err)
		return res, err
	}
	return r.Clusters, nil
}

func cliGetSettings() (Settings, error) {
	var r Settings
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + ""
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR in settings", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR in settings", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in settings", err)
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
		log.Println("ERROR in getting servers", err)
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
		log.Println("ERROR in getting master", err)
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
		log.Println("ERROR on getting logs ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR on getting logs", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR on getting logs", err)
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
		log.Println("ERROR", err)
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR", err)
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
		log.Println("ERROR", err)
		return "", err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR", err)
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", errors.New(string(body))
	}

	return string(body), nil
}
