package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/go-sql-driver/mysql"
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
	for k, url := range hostList {
		var err error
		servers[k], err = newServerMonitor(url)
		if verbose {
			log.Printf("DEBUG: Creating new server: %v", servers[k].URL)
		}
		if err != nil {
			if driverErr, ok := err.(*mysql.MySQLError); ok {
				if driverErr.Number == 1045 {
					return fmt.Errorf("ERROR: Database access denied: %s", err.Error())
				}
			}
			if verbose {
				log.Println("ERROR:", err)
			}
			log.Printf("INFO : Server %s is dead.", servers[k].URL)
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
	err := newServerList()
	if err != nil {
		return err
	}
	// If no slaves are detected, then bail out
	if len(slaves) == 0 {
		return errors.New("ERROR: No slaves were detected")
	}

	// Check that all slave servers have the same master.
	if multiMaster == false {
		for _, sl := range slaves {
			if sl.hasSiblings(slaves) == false {
				return topologyError{
					33,
					fmt.Sprintf("ERROR: Multiple masters were detected"),
				}
			}
		}
	} else {
		srw := 0
		for _, s := range servers {
			if s.ReadOnly == "OFF" {
				srw++
			}
		}
		if srw > 1 {
			return topologyError{
				11,
				fmt.Sprintf("ERROR: RW server count > 1 in multi-master mode. Please set slaves to RO (SET GLOBAL read_only=1)"),
			}
		}
	}

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
	// Final check if master has been found
	if master == nil {
		return topologyError{
			83,
			fmt.Sprintf("ERROR: Could not autodetect a master"),
		}
	}
	// End of autodetection code

	for _, sl := range slaves {
		if verbose {
			log.Printf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, master.Host)
		}
		if dbhelper.IsSlaveof(sl.Conn, sl.Host, master.IP) == false {
			log.Printf("WARN : Server %s is not a slave of declared master %s", master.URL, master.Host)
		}
		if sl.LogBin == "OFF" {
			return topologyError{
				81,
				fmt.Sprintf("ERROR: Binary log disabled on slave: %s", sl.URL),
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
