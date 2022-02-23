//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

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
				cliTlog = s18log.NewTermLog(loglen)
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
