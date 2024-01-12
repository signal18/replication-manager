// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(decryptCmd)
	keygenCmd.Flags().StringVar(&keyPath, "decrypt", "/etc/replication-manager/.replication-manager.key", "Decryption key file path")

}

var decryptCmd = &cobra.Command{
	Use:   "decrypt",
	Short: "Decrypt secret encoded fond inside configs",
	Long:  "It s better to hide password inside config file using keygen and password command",

	Run: func(cmd *cobra.Command, args []string) {
		RepMan = new(ReplicationManager)
		RepMan.CommandLineFlag = GetCommandLineFlag(cmd)
		RepMan.DefaultFlagMap = defaultFlagMap
		RepMan.InitConfig(conf)
		for cluster, conf := range RepMan.Confs {
			conf.Reveal(cluster, "/etc/replication-manager")
		}

	},
}
