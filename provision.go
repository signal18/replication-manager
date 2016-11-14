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
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"
)

var (
	source      string
	destination string
	cleanall    = false
)

func init() {
	rootCmd.AddCommand(bootstrapCmd)
	rootCmd.AddCommand(provisionCmd)
	provisionCmd.Flags().StringVar(&source, "source", "", "Source server")
	provisionCmd.Flags().StringVar(&destination, "destination", "", "Source server")
	bootstrapCmd.Flags().BoolVar(&cleanall, "clean-all", false, "Reset all slaves and binary logs before bootstrapping")
	bootstrapCmd.Flags().StringVar(&conf.PrefMaster, "prefmaster", "", "Preferred server for master initialization")
	bootstrapCmd.Flags().StringVar(&conf.MasterConn, "master-connection", "", "Connection name to use for multisource replication")
	bootstrapCmd.Flags().IntVar(&conf.MasterConnectRetry, "master-connect-retry", 10, "Specifies how many seconds to wait between slave connect retries to master")
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap a replication environment",
	Long:  `The bootstrap command is used to create a new replication environment from scratch`,
	Run: func(cmd *cobra.Command, args []string) {
		sme = new(state.StateMachine)
		sme.Init()
		repmgrFlagCheck()
		newServerList()
		err := bootstrap()
		if err != nil {
			log.Println(err)
		}
	},
}

func bootstrap() error {
	sme.SetFailoverState()
	if cleanall {
		log.Println("INFO : Cleaning up replication on existing servers")
		for _, server := range servers {
			err := dbhelper.SetDefaultMasterConn(server.Conn, conf.MasterConn)
			if err != nil {
				sme.RemoveFailoverState()
				return err
			}
			err = dbhelper.ResetMaster(server.Conn)
			if err != nil {
				sme.RemoveFailoverState()
				return err
			}
			err = dbhelper.StopAllSlaves(server.Conn)
			if err != nil {
				sme.RemoveFailoverState()
				return err
			}
			err = dbhelper.ResetAllSlaves(server.Conn)
			if err != nil {
				sme.RemoveFailoverState()
				return err
			}
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos=''")
			if err != nil {
				sme.RemoveFailoverState()
				return err
			}
		}
	} else {
		err := topologyDiscover()
		if err == nil {
			sme.RemoveFailoverState()
			return errors.New("ERROR: Environment already has an existing master/slave setup")
		}
	}
	masterKey := 0
	if conf.PrefMaster != "" {
		masterKey = func() int {
			for k, server := range servers {
				if server.URL == conf.PrefMaster {
					sme.RemoveFailoverState()
					return k
				}
			}
			sme.RemoveFailoverState()
			return -1
		}()
	}
	if masterKey == -1 {
		return errors.New("ERROR: Preferred master could not be found in existing servers")
	}
	_, err := servers[masterKey].Conn.Exec("RESET MASTER")
	if err != nil {
		logprint("WARN : RESET MASTER failed on master")
	}
	for key, server := range servers {
		if key == masterKey {
			dbhelper.FlushTables(server.Conn)
			dbhelper.SetReadOnly(server.Conn, false)
			continue
		} else {
			stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d", conf.MasterConn, servers[masterKey].IP, servers[masterKey].Port, rplUser, rplPass, conf.MasterConnectRetry)
			_, err := server.Conn.Exec(stmt)
			if err != nil {
				sme.RemoveFailoverState()
				return errors.New(fmt.Sprintln("ERROR:", stmt, err))
			}
			_, err = server.Conn.Exec("START SLAVE '" + conf.MasterConn + "'")
			if err != nil {
				sme.RemoveFailoverState()
				return errors.New(fmt.Sprintln("ERROR: Start slave: ", err))
			}
			dbhelper.SetReadOnly(server.Conn, true)
		}
	}
	logprintf("INFO : Environment bootstrapped with %s as master", servers[masterKey].URL)
	sme.RemoveFailoverState()
	return nil
}

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a replica server",
	Long: `The provision command is used to create a new replication server
using mysqldump or xtrabackup`,
	Run: func(cmd *cobra.Command, args []string) {
		dbHost, dbPort := misc.SplitHostPort(source)
		destHost, destPort := misc.SplitHostPort(destination)
		dbUser, dbPass = misc.SplitPair(conf.User)
		hostArg := fmt.Sprintf("--host=%s", dbHost)
		portArg := fmt.Sprintf("--port=%s", dbPort)
		userArg := fmt.Sprintf("--user=%s", dbUser)
		passArg := fmt.Sprintf("--password=%s", dbPass)
		desthostArg := fmt.Sprintf("--host=%s", destHost)
		destportArg := fmt.Sprintf("--port=%s", destPort)
		dumpCmd := exec.Command("/usr/bin/mysqldump", "--opt", "--single-transaction", "--all-databases", hostArg, portArg, userArg, passArg)
		clientCmd := exec.Command("/usr/bin/mysql", desthostArg, destportArg, userArg, passArg)
		var err error
		clientCmd.Stdin, err = dumpCmd.StdoutPipe()
		if err != nil {
			log.Fatal("Error opening pipe:", err)
		}
		if err := dumpCmd.Start(); err != nil {
			log.Fatal("Error starting dump:", err, dumpCmd.Path, dumpCmd.Args)
		}
		if err := clientCmd.Run(); err != nil {
			log.Fatal("Error starting client:", err, clientCmd.Path, clientCmd.Args)
		}
	},
}
