package main

import (
	"fmt"
	"log"
	"os/exec"

	"github.com/spf13/cobra"
)

var (
	source      string
	destination string
)

func init() {
	rootCmd.AddCommand(provisionCmd)
	provisionCmd.Flags().StringVar(&source, "source", "", "Source server")
	provisionCmd.Flags().StringVar(&destination, "destination", "", "Source server")
}

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provisions a server",
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
