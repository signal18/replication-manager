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
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var configuratorCmd = &cobra.Command{
	Use:   "configurator",
	Short: "Config generator",
	Long:  `Config generator produce tar.gz for databases and proxies based on ressource and tags description`,
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
		termboxChan := cliNewTbChan()
		interval := time.Second
		ticker := time.NewTicker(interval * time.Duration(2))
		var conf config.Config
		var configurator configurator.Configurator
		configurator.Init(conf)
		for cliExit == false {
			select {
			case <-ticker.C:

				cliDisplayConfigurator(configurator)
			case event := <-termboxChan:
				switch event.Type {
				case termbox.EventKey:
					if event.Key == termbox.KeyCtrlS {

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
					}
					if event.Key == termbox.KeyCtrlW {
					}
					if event.Key == termbox.KeyCtrlI {
					}
					if event.Key == termbox.KeyCtrlV {
					}
					if event.Key == termbox.KeyCtrlE {
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
	},
}

func cliDisplayConfigurator(configurator *configurator.Configurator) {

	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" Replication Manager Configurator")

	cliPrintfTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, headstr)
	cliPrintfTb(0, 1, termbox.ColorRed, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, cliConfirm)
	cliPrintfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%1s%15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", " ", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	cliTlog.Line = 3

	cliTlog.Line++

	cliPrintTb(0, cliTlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-F to failover, Ctrl-(N|P) to change Cluster,Ctrl-H to help")

	cliTlog.Line = cliTlog.Line + 3
	cliTlog.Print()

	termbox.Flush()

}
