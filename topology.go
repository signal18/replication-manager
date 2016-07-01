package main

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/go-sql-driver/mysql"
	"github.com/mariadb-corporation/replication-manager/state"
	"github.com/spf13/cobra"
	"github.com/tanji/mariadb-tools/dbhelper"
)

type topologyError struct {
	Code int
	Msg  string
}

func init() {
	rootCmd.AddCommand(topologyCmd)
	topologyCmd.Flags().BoolVar(&multiMaster, "multimaster", false, "Turn on multi-master detection")
}

func newServerList() {
	servers = make([]*ServerMonitor, len(hostList))
	for k, url := range hostList {
		var err error
		servers[k], err = newServerMonitor(url)
		if err != nil {
			log.Fatalf("ERROR: Could not open connection to server %s : %s", servers[k].URL, err)
		}
		if verbose {
			logprintf("DEBUG: New server created: %v.", servers[k].URL)
		}
	}
}

func pingServerList() {
	wg := new(sync.WaitGroup)
	mx := new(sync.Mutex)
	for _, sv := range servers {
		wg.Add(1)
		go func(sv *ServerMonitor) {
			defer wg.Done()
			err := sv.Conn.Ping()
			if err != nil {
				if driverErr, ok := err.(*mysql.MySQLError); ok {
					if driverErr.Number == 1045 {
						sv.State = stateUnconn
						mx.Lock()
						sme.AddState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Database %s access denied: %s.", sv.URL, err.Error()), ErrFrom: "TOPO"})
						mx.Unlock()
					}
				} else {
					mx.Lock()
					sme.AddState("INF00001", state.State{ErrType: "INFO", ErrDesc: fmt.Sprintf("INFO : Server %s is dead.", sv.URL), ErrFrom: "TOPO"})
					mx.Unlock()
					sv.State = stateFailed
				}
			}
		}(sv)
	}
	wg.Wait()
}

// Start of topology detection
// Create a connection to each host and build list of slaves.
func topologyDiscover() error {
	slaves = nil
	for _, sv := range servers {
		if sv.State == stateFailed {
			continue
		}
		sv.refresh()
		if sv.UsingGtid != "" {
			if loglevel > 2 {
				logprintf("DEBUG: Server %s is configured as a slave", sv.URL)
			}
			sv.State = stateSlave
			slaves = append(slaves, sv)
		} else {
			if loglevel > 2 {
				logprintf("DEBUG: Server %s is not a slave. Setting aside", sv.URL)
			}
			sv.State = stateUnconn
		}
		// Check user privileges on live servers
		if sv.State != stateFailed {
			priv, err := dbhelper.GetPrivileges(sv.Conn, dbUser, sv.Host)
			if err != nil {
				sme.AddState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting privileges for user %s on host %s: %s.", dbUser, sv.Host, err), ErrFrom: "CONF"})
			}
			if priv.Repl_client_priv == "N" {
			}
			if priv.Repl_slave_priv == "N" {
				sme.AddState("ERR00007", state.State{ErrType: "ERROR", ErrDesc: "User must have REPLICATION_SLAVE privilege.", ErrFrom: "CONF"})
			}
			if priv.Super_priv == "N" {
				sme.AddState("ERR00008", state.State{ErrType: "ERROR", ErrDesc: "User must have SUPER privilege.", ErrFrom: "CONF"})
			}
		}
	}

	// If no slaves are detected, generate an error
	if len(slaves) == 0 {
		sme.AddState("ERR00010", state.State{ErrType: "ERROR", ErrDesc: "No slaves were detected.", ErrFrom: "TOPO"})
	}

	// Check that all slave servers have the same master.
	if multiMaster == false {
		for _, sl := range slaves {

			if sl.hasSiblings(slaves) == false {
				sme.AddState("ERR00011", state.State{ErrType: "WARNING", ErrDesc: "Multiple masters were detected, auto switching to multimaster monitoring.", ErrFrom: "TOPO"})

				multiMaster = true
			}
		}
	}
	if multiMaster == true {
		srw := 0
		for _, s := range servers {
			if s.ReadOnly == "OFF" {
				srw++
			}
		}
		if srw > 1 {
			sme.AddState("WARN00003", state.State{ErrType: "WARNING", ErrDesc: "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, switching to prefered master.", ErrFrom: "TOPO"})
		}
		srw = 0
		for _, s := range servers {
			if s.ReadOnly == "ON" {
				srw++
			}
		}
		if srw > 1 {
			sme.AddState("WARN00004", state.State{ErrType: "WARNING", ErrDesc: "RO server count > 1 in multi-master mode.  switching to prefered master.", ErrFrom: "TOPO"})
			// 		    server:=GetPreferedMaster()
			//			    dbhelper.SetReadOnly(server.Conn, true)

		}
	}

	if slaves != nil {
		// Depending if we are doing a failover or a switchover, we will find the master in the list of
		// failed hosts or unconnected hosts.
		// First of all, get a server id from the slaves slice, they should be all the same
		sid := slaves[0].MasterServerID
		for k, s := range servers {
			if multiMaster == false && s.State == stateUnconn {
				if s.ServerID == sid {
					master = servers[k]
					master.State = stateMaster
					if loglevel > 2 {
						logprintf("DEBUG: Server %s was autodetected as a master", s.URL)
					}
					break
				}
			}
			if multiMaster == true {
				if s.ReadOnly == "OFF" {
					master = servers[k]
					master.State = stateMaster
					if loglevel > 2 {
						logprintf("DEBUG: Server %s was autodetected as a master", s.URL)
					}
					break
				}
			}
		}

		// If master is not initialized, find it in the failed hosts list
		if master == nil {
			// Slave master_host variable must point to failed master
			smh := slaves[0].MasterHost
			for k, s := range servers {
				if s.State == stateFailed {
					if s.Host == smh || s.IP == smh {
						master = servers[k]
						master.PrevState = stateMaster
						if loglevel > 2 {
							logprintf("DEBUG: Assuming failed server %s was a master", s.URL)
						}
						break
					}
				}
			}
		}
	}
	// Final check if master has been found
	if master == nil {

		sme.AddState("ERR00012", state.State{ErrType: "ERROR", ErrDesc: "Could not autodetect a master.", ErrFrom: "TOPO"})
		master.RplMasterStatus=false

	
	} else {

		// End of autodetection code
		if multiMaster == false {
			for _, sl := range slaves {
				if loglevel > 2 {
					logprintf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, master.Host)
				}
				if dbhelper.IsSlaveof(sl.Conn, sl.Host, master.IP) == false {
					logprintf("WARN : Server %s is not a slave of declared master %s", master.URL, master.Host)
				}
				if sl.LogBin == "OFF" {
					sme.AddState("ERR00013", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Binary log disabled on slave: %s.", sl.URL), ErrFrom: "TOPO"})
				}
				if sl.Delay.Int64  < maxDelay {
				   master.RplMasterStatus=true
				}
			}
		}
		sme.SetMasterUpAndSync(master.SemiSyncMasterStatus, master.RplMasterStatus)
	}
	if sme.CanMonitor() {
		return nil
	}
	return errors.New("Error found in State Machine Engine")
}

func printTopology() {
	for k, v := range servers {
		logprintf("DEBUG: Server [%d] %s %s %s", k, v.URL, v.State, v.PrevState)
	}
}

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Print replication topology",
	Long:  `Print the replication topology by detecting master and slaves`,
	Run: func(cmd *cobra.Command, args []string) {
		repmgrFlagCheck()
		newServerList()
		err := topologyDiscover()
		if err != nil {
			log.Fatalln(err)
		}
		for _, v := range servers {
			fmt.Println(v.URL, v.State)
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		for _, server := range servers {
			defer server.Conn.Close()
		}
	},
}
