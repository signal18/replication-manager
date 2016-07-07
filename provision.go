package main

import (
	"errors"
	"fmt"
	"log"
	"os/exec"

	"github.com/mariadb-corporation/replication-manager/state"
	"github.com/spf13/cobra"
	"github.com/tanji/mariadb-tools/dbhelper"
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
	bootstrapCmd.Flags().StringVar(&prefMaster, "prefmaster", "", "Preferred server for master initialization")
	bootstrapCmd.Flags().StringVar(&masterConn, "master-connection", "", "Connection name to use for multisource replication")
	bootstrapCmd.Flags().IntVar(&masterConnectRetry, "master-connect-retry", 10, "Specifies how many seconds to wait between slave connect retries to master")
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
	if cleanall {
		log.Println("INFO : Cleaning up replication on existing servers")
		for _, server := range servers {
			err := dbhelper.SetDefaultMasterConn(server.Conn, masterConn)
			if err != nil {
				return err
			}
			err = dbhelper.ResetMaster(server.Conn)
			if err != nil {
				return err
			}
			err = dbhelper.StopAllSlaves(server.Conn)
			if err != nil {
				return err
			}
			err = dbhelper.ResetAllSlaves(server.Conn)
			if err != nil {
				return err
			}
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos=''")
			if err != nil {
				return err
			}
		}
	} else {
		err := topologyDiscover()
		if err == nil {
			return errors.New("ERROR: Environment already has an existing master/slave setup")
		}
	}
	masterKey := 0
	if prefMaster != "" {
		masterKey = func() int {
			for k, server := range servers {
				if server.URL == prefMaster {
					return k
				}
			}
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
			stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d", masterConn, servers[masterKey].IP, servers[masterKey].Port, rplUser, rplPass, masterConnectRetry)
			_, err := server.Conn.Exec(stmt)
			if err != nil {
				return errors.New(fmt.Sprintln("ERROR:", stmt, err))
			}
			_, err = server.Conn.Exec("START SLAVE '" + masterConn + "'")
			if err != nil {
				return errors.New(fmt.Sprintln("ERROR: Start slave: ", err))
			}
			dbhelper.SetReadOnly(server.Conn, true)
		}
	}
	logprintf("INFO : Environment bootstrapped with %s as master", servers[masterKey].URL)
	return nil
}

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a replica server",
	Long: `The provision command is used to create a new replication server
using mysqldump or xtrabackup`,
	Run: func(cmd *cobra.Command, args []string) {
		dbHost, dbPort := splitHostPort(source)
		destHost, destPort := splitHostPort(destination)
		dbUser, dbPass = splitPair(user)
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
