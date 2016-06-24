package main

import (
	"fmt"
	"log"

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

func (e topologyError) Error() string {
	return fmt.Sprintf("%v [#%v]", e.Msg, e.Code)
}

func newServerList() error {

	servers = make([]*ServerMonitor, len(hostList))
	slaves = nil
	master = nil
	for k, url := range hostList {
		var err error
		servers[k], err = newServerMonitor(url)
		if verbose {
			log.Printf("DEBUG: Creating new server: %v.", servers[k].URL)
		}
		if err != nil {
			if driverErr, ok := err.(*mysql.MySQLError); ok {
				if driverErr.Number == 1045 {
					servers[k].State = stateUnconn
					sme.AddState("ERR00009", state.State{"ERROR", fmt.Sprintf("Database %s access denied: %s.", servers[k].URL, err.Error()), "TOPO"})
				}
			}
			sme.AddState("INF00001", state.State{"INFO", fmt.Sprintf("INFO : Server %s is dead.", servers[k].URL), "TOPO"})
			servers[k].State = stateFailed
			continue
		}
		if verbose {
			log.Printf("DEBUG: Checking if server %s is slave", servers[k].URL)
		}
		servers[k].refresh()
		if servers[k].UsingGtid != "" {
			if verbose {
				log.Printf("DEBUG: Server %s is configured as a slave", servers[k].URL)
			}
			servers[k].State = stateSlave
			slaves = append(slaves, servers[k])
		} else {
			if verbose {
				log.Printf("DEBUG: Server %s is not a slave. Setting aside", servers[k].URL)
			}
			servers[k].State = stateUnconn
		}
	}
	return nil
}

// Start of topology detection
// Create a connection to each host and build list of slaves.
func topologyInit() error {
	newServerList()

    // Check user privileges on live servers
	for _, sv := range servers {
		if sv.State != stateFailed {
			priv, err := dbhelper.GetPrivileges(sv.Conn, dbUser, sv.Host)
			if err != nil {
				sme.AddState("ERR00005", state.State{"ERROR", fmt.Sprintf("Error getting privileges for user %s on host %s: %s.", dbUser, sv.Host, err), "CONF"})
			}
			if priv.Repl_client_priv == "N" {
			}
			if priv.Repl_slave_priv == "N" {
				sme.AddState("ERR00007", state.State{"ERROR", "User must have REPLICATION_SLAVE privilege.", "CONF"})
			}
			if priv.Super_priv == "N" {
				sme.AddState("ERR00008", state.State{"ERROR", "User must have SUPER privilege.", "CONF"})
			}
		}
	}

	// If no slaves are detected, then bail out
	if len(slaves) == 0 {
		sme.AddState("ERR00010", state.State{"ERROR", "No slaves were detected.", "TOPO"})
	}

	// Check that all slave servers have the same master.
	if multiMaster == false {
		for _, sl := range slaves {

			if sl.hasSiblings(slaves) == false {
				sme.AddState("ERR00011", state.State{"WARNING", "Multiple masters were detected, auto switching to multimaster monitoring.", "TOPO"})

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
			sme.AddState("WARN00003", state.State{"WARNING", "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, switching to prefered master.", "TOPO"})
		}
		srw = 0
		for _, s := range servers {
			if s.ReadOnly == "ON" {
				srw++
			}
		}
		if srw > 1 {
			sme.AddState("WARN00004", state.State{"WARNING", "RO server count > 1 in multi-master mode.  switching to prefered master.", "TOPO"})
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
				if verbose {
					log.Printf("DEBUG: Server %s was autodetected as a master", s.URL)
				}
				break
			}
		}
		if multiMaster == true {
			if s.ReadOnly == "OFF" {
				master = servers[k]
				master.State = stateMaster
				if verbose {
					log.Printf("DEBUG: Server %s was autodetected as a master", s.URL)
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
					if verbose {
						log.Printf("DEBUG: Assuming failed server %s was a master", s.URL)
					}
					break
				}
			}
		}
	}
	}
	// Final check if master has been found
	if master == nil {
		
		sme.AddState("ERR00012", state.State{"ERROR", "Could not autodetect a master.", "TOPO"})

	} else {
	
	// End of autodetection code
	if multiMaster == false {
		for _, sl := range slaves {
			if verbose {
				log.Printf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, master.Host)
			}
			if dbhelper.IsSlaveof(sl.Conn, sl.Host, master.IP) == false {
				log.Printf("WARN : Server %s is not a slave of declared master %s", master.URL, master.Host)
			}
		  if sl.LogBin == "OFF" {
		   	
			sme.AddState("ERR00013", state.State{"ERROR", fmt.Sprintf("Binary log disabled on slave: %s.", sl.URL), "TOPO"})
		  }	
		}
	}
	}
	if verbose {
		printTopology()
	}
	return nil
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
		err := topologyInit()
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
