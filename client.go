// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Author: Stephane Varoqui <stephane@mariadb.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	termbox "github.com/nsf/termbox-go"
	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/termlog"
)

var (
	cliUser         string
	cliPassword     string
	cliHost         string
	cliPort         string
	cliCert         string
	cliNoCheckCert  bool
	cliToken        string
	cliClusters     []string
	cliClusterIndex int
	cliTlog         termlog.TermLog
	cliTermlength   int
	cliServers      []cluster.ServerMonitor
	cliMaster       cluster.ServerMonitor
	cliSettings     Settings
)

var cliConn = http.Client{
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

func init() {
	rootCmd.AddCommand(clientCmd)
	rootCmd.AddCommand(switchoverCmd)
	rootCmd.AddCommand(failoverCmd)
	clientCmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	clientCmd.Flags().StringVar(&cliPassword, "password", "mariadb", "Paswword of replication-manager")
	clientCmd.Flags().StringVar(&cliPort, "port", "3000", "TLS port of  replication-manager")
	clientCmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	clientCmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	clientCmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")
}

var failoverCmd = &cobra.Command{
	Use:   "failover",
	Short: "Failover a dead master",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {

		currentCluster = new(cluster.Cluster)
		tlog := termlog.TermLog{}
		err := currentCluster.Init(conf, cfgGroup, &tlog, termlength, runUUID, Version, repmgrHostname, nil)
		if err != nil {
			log.WithError(err).Fatal("Error initializing cluster")
		}
		currentCluster.SetLogStdout()
		currentCluster.TopologyDiscover()
		currentCluster.FailoverForce()

	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		currentCluster.Close()
	},
}

var switchoverCmd = &cobra.Command{
	Use:   "switchover",
	Short: "Perform a master switch",
	Long: `Performs an online master switch by promoting a slave to master
and demoting the old master to slave`,
	Run: func(cmd *cobra.Command, args []string) {
		currentCluster = new(cluster.Cluster)
		tlog := termlog.TermLog{}
		err := currentCluster.Init(confs[cfgGroup], cfgGroup, &tlog, termlength, runUUID, Version, repmgrHostname, nil)
		if err != nil {
			log.WithError(err).Fatal("E:rror initializing cluster")
		}
		currentCluster.SetLogStdout()
		currentCluster.TopologyDiscover()
		time.Sleep(time.Millisecond * 3000)
		currentCluster.MasterFailover(false)
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		currentCluster.Close()
	},
}

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Starts the interactive replication-manager client",
	Long:  "Connect to replication-manager in stateful TLS JWT mode.",
	Run: func(cmd *cobra.Command, args []string) {

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
		cliToken, _ = cliLogin()
		cliClusters = cliGetClusters()
		loglen := cliTermlength - 9 - (len(strings.Split(conf.Hosts, ",")) * 3)
		cliTlog = termlog.NewTermLog(loglen)
		termboxChan := cliNewTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * time.Duration(conf.MonitoringTicker))
		exitMsg = cliToken

		for exit == false {
			select {
			case <-ticker.C:
				cliSettings, _ = cliGetSettings()
				cliServers, _ = cliGetServers()
				cliMaster, _ = cliGetMaster()
				cliTlog.Buffer, _ = cliGetLogs()
				cliDisplay()
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						cliClusterCmd("switchover")
					}
					if event.Key == termbox.KeyCtrlF {
						if cliMaster.State == "Failed" {
							cliClusterCmd("failover")
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
						// call reaonly
					}
					if event.Key == termbox.KeyCtrlW {
						// call reaonly off
					}
					if event.Key == termbox.KeyCtrlI {
						cliClusterCmd("interactive")
					}
					if event.Key == termbox.KeyCtrlH {
						cliDisplayHelp()
					}
					if event.Key == termbox.KeyCtrlQ {

						exit = true

					}
					if event.Key == termbox.KeyCtrlC {

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

func cliGetClusters() []string {
	var cl []string
	log.Printf("config group:%s", cfgGroup)
	if cfgGroup != "" {
		cl = strings.Split(cfgGroup, ",")
	} else {
		cl, _ = cliGetAllClusters()
		//		log.Printf("%s", cl)
	}
	return cl
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
		cliPrintfTb(0, cliTlog.Line, fgCol, termbox.ColorBlack, "%15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", server.Host, server.Port, server.State, server.FailCount, server.UsingGtid, gtidCurr, gtidSlave, "", server.Delay.Int64, server.ReadOnly)
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

func cliDisplayHelp() {
	cliLogPrint("HELP : Ctrl-D  Print debug information")
	cliLogPrint("HELP : Ctrl-F  Manual Failover")
	cliLogPrint("HELP : Ctrl-I  Toggle automatic/manual failover mode")
	cliLogPrint("HELP : Ctrl-R  Set slaves read-only")
	cliLogPrint("HELP : Ctrl-S  Switchover")
	cliLogPrint("HELP : Ctrl-N  Next Cluster")
	cliLogPrint("HELP : Ctrl-P  Previous Cluster")
	cliLogPrint("HELP : Ctrl-Q  Quit")
	cliLogPrint("HELP : Ctrl-C  Quit")
	cliLogPrint("HELP : Ctrl-W  Set slaves read-write")
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

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	return r, nil
}

func cliGetServers() ([]cluster.ServerMonitor, error) {
	var r []cluster.ServerMonitor
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/servers"
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
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/master"
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
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/logs"
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

func cliClusterCmd(command string) error {
	//var r string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/" + command
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", bearer)

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
