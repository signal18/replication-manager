//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	termbox "github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/utils/s18log"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var clientConsoleCmd = &cobra.Command{
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
		//	loglen := cliTermlength - 9 - (len(strings.Split(conf.Hosts, ",")) * 3)
		termboxChan := cliNewTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * time.Duration(2))

		for cliExit == false {
			select {
			case <-ticker.C:
				cliSettings, _ = cliGetSettings()
				cliServers, _ = cliGetServers()
				loglen := cliTermlength - 9 - (len(cliServers) * 3)
				cliMaster, _ = cliGetMaster()
				dlogs, _ := cliGetLogs()
				cliTlog = s18log.NewTermLog(loglen)
				cliAddTlog(dlogs)
				cliDisplayMonitor()
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {
						//	fmt.Println("Confirm switchover ? [Y,y]")
						cliConfirm = "Confirm switchover ? [Y,y]"

						cliDisplayMonitor()
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
						cliClusterCmd("settings/actions/switch/failover-readonly-state", nil)
					}
					if event.Key == termbox.KeyCtrlW {
						cliClusterCmd("settings/actions/switch/failover-readonly-state", nil)
					}
					if event.Key == termbox.KeyCtrlI {
						cliClusterCmd("settings/actions/switch/failover-mode", nil)
					}
					if event.Key == termbox.KeyCtrlV {
						cliClusterCmd("settings/actions/switch/verbosity", nil)
					}
					if event.Key == termbox.KeyCtrlE {
						cliClusterCmd("actions/reset-failover-control", nil)
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

func cliDisplayMonitor() {

	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" Replication Manager Client ")

	if cliClusters[cliClusterIndex] != "" {
		headstr += fmt.Sprintf("| Group: %s", cliClusters[cliClusterIndex])
	}
	if cliSettings.Conf.Interactive == false {
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

func cliLogPrint(msg ...interface{}) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))

	if cliTlog.Len > 0 {
		s := fmt.Sprint(stamp, "[", cliClusters[cliClusterIndex], "] ", fmt.Sprint(msg...))
		cliTlog.Add(s)
		cliDisplayMonitor()
	}

}
