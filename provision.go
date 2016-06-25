package main

import (
	"fmt"
	"log"
	"os/exec"
	"github.com/spf13/cobra"
	"github.com/tanji/mariadb-tools/dbhelper"
	"github.com/mariadb-corporation/replication-manager/state"
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

}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap a replication environment",
	Long:  `The bootstrap command is used to create a new replication environment from scratch`,
	Run: func(cmd *cobra.Command, args []string) {
		sme = new(state.StateMachine)
		sme.Init()
		repmgrFlagCheck()
		if cleanall {
			log.Println("INFO : Cleaning up replication on existing servers")
			err := newServerList()
			if err != nil {
				log.Fatal(err)
			}
			for _, server := range servers {
				err = dbhelper.SetDefaultMasterConn(server.Conn, masterConn)
				if err != nil {
					log.Fatal(err)
				}
				err = dbhelper.ResetMaster(server.Conn)
				if err != nil {
					log.Fatal(err)
				}
				err = dbhelper.StopAllSlaves(server.Conn)
				if err != nil {
					log.Fatal(err)
				}
				err = dbhelper.ResetAllSlaves(server.Conn)
				if err != nil {
					log.Fatal(err)
				}
			}
		} else {
			err := topologyInit()
			if err == nil {
				log.Fatal("ERROR: Environment already has an existing master/slave setup")
			}
			if topologyErr, ok := err.(*topologyError); ok {
				if topologyErr.Code != 81 {
					log.Fatal(err)
				}
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
			log.Fatal("ERROR: Preferred master could not be found in existing servers")
		}
		servers[masterKey].Conn.Exec("RESET MASTER")
		for key, server := range servers {
			if key == masterKey {
				continue
			} else {
				stmt := "CHANGE MASTER '" + masterConn + "' TO master_host='" + servers[masterKey].IP + "', master_port=" + servers[masterKey].Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "', master_use_gtid=current_pos"
				_, err := server.Conn.Exec(stmt)
				if err != nil {
					log.Fatal(stmt, err)
				}
				_, err = server.Conn.Exec("START SLAVE '" + masterConn + "'")
				if err != nil {
					log.Fatal("Start slave: ", err)
				}
			}
		}
		log.Printf("INFO : Environment bootstrapped with %s as master", servers[masterKey].URL)
	},
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
