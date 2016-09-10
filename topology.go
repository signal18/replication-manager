// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"errors"
	"fmt"
	"log"
	"sync"
  "strings"
	"github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/state"
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
			tlog.Add(fmt.Sprintf("DEBUG: New server created: %v", servers[k].URL))
		}
	}
	// Spider shard discover
	if spider == true {
	for _, s := range servers {
		tlog.Add(fmt.Sprintf("INFO: Is Spider Monitor server %s ", s.URL))
		mon,err:=  dbhelper.GetSpiderMonitor(s.Conn)

		if err == nil {
			if mon!= "" {
				tlog.Add(fmt.Sprintf("INFO: Retriving Spider Shards Server %s ", s.URL))
				extra_url,err:=   dbhelper.GetSpiderShardUrl(s.Conn)
				if err == nil {
					if extra_url!= "" {

						for j, url := range strings.Split(extra_url, ",") {
							var err error
							srv ,err := newServerMonitor(url)
							srv.State=stateShard

							servers =  append(servers,srv)
							if err != nil {
								log.Fatalf("ERROR: Could not open connection to Spider Shard server %s : %s", servers[j].URL, err)
							}
							if verbose {
								tlog.Add(fmt.Sprintf("DEBUG: New server created: %v", servers[j].URL))
							}
						}
					}
				}
			}
		}
	}
	}
}

func pingServerList() {
	wg := new(sync.WaitGroup)
	for _, sv := range servers {
		wg.Add(1)
		go func(sv *ServerMonitor) {
			defer wg.Done()
			err := sv.Conn.Ping()
			if err != nil {
				if driverErr, ok := err.(*mysql.MySQLError); ok {
					if driverErr.Number == 1045 {
						sv.State = stateUnconn
						sme.AddState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Database %s access denied: %s.", sv.URL, err.Error()), ErrFrom: "TOPO"})
					}
				} else {
					sme.AddState("INF00001", state.State{ErrType: "INFO", ErrDesc: fmt.Sprintf("Server %s is down", sv.URL), ErrFrom: "TOPO"})
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
	for k, sv := range servers {
		err := sv.refresh()
		if err != nil {
			if loglevel > 2 {
				logprintf("DEBUG: Server %s could not be refreshed: %s", sv.URL, err)
			}
			continue
		}
		if sv.UsingGtid != "" {
			if loglevel > 2 {
				logprintf("DEBUG: Server %s is configured as a slave", sv.URL)
			}
			sv.State = stateSlave
			slaves = append(slaves, sv)
		} else {
			var n int
			err := sv.Conn.Get(&n, "SELECT COUNT(*) AS n FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command='binlog dump'")
			if err != nil {
				sme.AddState("ERR00014", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting binlog dump count on server %s: %s", sv.URL, err), ErrFrom: "CONF"})
				sv.State = stateFailed
				continue
			}
			if n == 0 {
				sv.State = stateUnconn
				// TODO: fix flapping in case slaves are reconnecting
				if loglevel > 2 {
					logprintf("DEBUG: Server %s has no slaves connected", sv.URL)
				}
			} else {
				master = servers[k]
				master.State = stateMaster
			}
		}
		// Check user privileges on live servers
		if loglevel > 2 {
			logprintf("DEBUG: Check loop on %s", sv.URL)
		}
		if sv.State != stateFailed {
			priv, err := dbhelper.GetPrivileges(sv.Conn, dbUser, sv.Host)
			if err != nil {
				sme.AddState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting privileges for user %s on server %s: %s.", dbUser, sv.URL, err), ErrFrom: "CONF"})
			}
			if priv.Repl_client_priv == "N" {
				sme.AddState("ERR00006", state.State{ErrType: "ERROR", ErrDesc: "User must have REPLICATION CLIENT privilege.", ErrFrom: "CONF"})
			}
			if priv.Super_priv == "N" {
				sme.AddState("ERR00008", state.State{ErrType: "ERROR", ErrDesc: "User must have SUPER privilege.", ErrFrom: "CONF"})
			}
			if priv.Reload_priv == "N" {
				sme.AddState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: "User must have RELOAD privilege.", ErrFrom: "CONF"})
			}
			// Check replication user has correct privs.
			rpriv, err := dbhelper.GetPrivileges(sv.Conn, rplUser, sv.Host)
			if err != nil {
				sme.AddState("ERR00015", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting privileges for user %s on server %s: %s.", rplUser, sv.URL, err), ErrFrom: "CONF"})
			}
			if rpriv.Repl_slave_priv == "N" {
				sme.AddState("ERR00007", state.State{ErrType: "ERROR", ErrDesc: "User must have REPLICATION SLAVE privilege.", ErrFrom: "CONF"})
			}
			// Additional health checks go here
			if sv.acidTest() == false && sme.IsDiscovered() {
				sme.AddState("WARN00006", state.State{ErrType: "WARN", ErrDesc: "At least one server is not ACID-compliant. Please check that the values of sync_binlog and innodb_flush_log_at_trx_commit are set to 1", ErrFrom: "CONF"})
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
			server:=getPreferedMaster()
      if server != nil {
			  dbhelper.SetReadOnly(server.Conn, false)
			} else
			{
				sme.AddState("WARN00006", state.State{ErrType: "WARNING", ErrDesc: "Multi-master need a prefered master.", ErrFrom: "TOPO"})
		  }
 	  }
	} else if readonly {
		// In non-multimaster mode, enforce read-only flag if the option is set
		for _, s := range slaves {
			if s.ReadOnly == "OFF" && spider == false {
				dbhelper.SetReadOnly(s.Conn, true)
			}
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
			if multiMaster == true && servers[k].State != stateFailed {
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

	} else {
		master.RplMasterStatus = false
		// End of autodetection code
		if multiMaster == false {
			for _, sl := range slaves {
				if loglevel > 2 {
					logprintf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, master.Host)
				}
				if dbhelper.IsSlaveof(sl.Conn, sl.Host, master.IP) == false {
					sme.AddState("WARN00005", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Server %s is not a slave of declared master %s", master.URL, master.Host), ErrFrom: "TOPO"})
				}
				if sl.LogBin == "OFF" {
					sme.AddState("ERR00013", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Binary log disabled on slave: %s.", sl.URL), ErrFrom: "TOPO"})
				}
				if sl.Delay.Int64 <= maxDelay && sl.SQLThread == "Yes" {
					master.RplMasterStatus = true
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

func getPreferedMaster() *ServerMonitor {
	for _, server := range servers {
		if loglevel > 2 {
			logprintf("DEBUG: Server %s was lookup if prefered master: %s", server.URL, prefMaster)
		}
		if server.URL == prefMaster {
			return server
		}
	}
	return nil
}

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Print replication topology",
	Long:  `Print the replication topology by detecting master and slaves`,
	Run: func(cmd *cobra.Command, args []string) {
		sme = new(state.StateMachine)
		sme.Init()
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
