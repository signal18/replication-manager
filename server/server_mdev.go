//go:build !clients
// +build !clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"

	"github.com/spf13/cobra"
)

var mdevCsv string
var verbose bool

func init() {
	rootCmd.AddCommand(mdevUpdateCmd)
	mdevUpdateCmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite JSON records with latest CSV file")
	mdevUpdateCmd.Flags().BoolVar(&verbose, "debug", false, "Debug line per line")
	mdevUpdateCmd.Flags().StringVar(&mdevCsv, "csv-path", "/usr/share/replication-manager/repo/mdev.csv", "MDEV list csv file")
}

var mdevUpdateCmd = &cobra.Command{
	Use:   "mdev",
	Short: "Update MDEV blocker list",
	Long:  `Update MDEV blocker list by merging the issues from csv file with existing list in the MDEV JSON file.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Start mdev command !\n")
		RepMan = new(ReplicationManager)
		RepMan.CommandLineFlag = GetCommandLineFlag(cmd)
		RepMan.DefaultFlagMap = defaultFlagMap
		RepMan.InitConfig(conf)
		fmt.Printf("Config : %s\n", RepMan.Conf.ConfigFile)
		fmt.Printf("Verbose : %v\n", verbose)
		err := RepMan.Conf.UpdateMDevJSONFile(mdevCsv, overwrite, verbose)
		if err != nil {
			fmt.Printf("Config mdev update command fail: %s\n", err)
			return
		}
		fmt.Println("Success executing mdev update command!")
	},
}
