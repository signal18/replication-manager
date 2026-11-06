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
	"os"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(decryptCmd)
	keygenCmd.Flags().StringVar(&keyPath, "decrypt", "/etc/replication-manager/.replication-manager.key", "Decryption key file path")
	ensureRepMan()
}

var decryptCmd = &cobra.Command{
	Use:   "decrypt",
	Short: "Decrypt secret encoded fond inside configs",
	Long:  "It s better to hide password inside config file using keygen and password command",

	Run: func(cmd *cobra.Command, args []string) {
		// who may print every secret in clear: root on a deployed system (its key is in
		// /etc/replication-manager); with the self-contained binary (embed), the user it runs as,
		// its key being in that user's ~/.config/replication-manager. Checked before the
		// configuration is loaded, which creates directories.
		if os.Geteuid() != 0 && WithEmbed != "ON" {
			fmt.Fprintln(os.Stderr, "decrypt: refused, it prints every secret in clear: run it as root")
			os.Exit(1)
		}
		RepMan.CommandLineFlag = GetCommandLineFlag(cmd)
		RepMan.DefaultFlagMap = defaultFlagMap
		// decrypt never creates a key: loading the configuration generates one when none is found
		// where this user looks, and every value would then "decrypt" to garbage with it
		RepMan.ReadOnlyCommand = true // the load only reads: no key, no directory, no legacy file removed
		RepMan.InitConfig(conf, false)
		c := RepMan.Conf
		if _, err := c.LoadEncrytionKey(); err != nil {
			fmt.Fprintf(os.Stderr, "decrypt: the encryption key %s cannot be read: %s\n", c.MonitoringKeyPath, err)
			os.Exit(1)
		}

		// output files of older releases of this command: removed, and said
		removed, errs := config.RemoveRevealFiles(config.RevealOutputDir)
		if len(removed) > 0 {
			fmt.Fprintf(os.Stderr, "Removed %d output file(s) of an older decrypt: %s\n", len(removed), strings.Join(removed, ", "))
		}
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "Not removed: %s\n", err)
		}

		// the files the configuration load just read (ConfigPathList), as written, then each
		// cluster's effective values (environment and flags included): to the terminal only
		if len(RepMan.ConfigPathList) == 0 {
			fmt.Fprintln(os.Stderr, "decrypt: no configuration file was read")
		}
		if err := c.Reveal(os.Stdout, RepMan.ConfigPathList); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		clusters := make([]string, 0, len(RepMan.Confs))
		for name := range RepMan.Confs {
			clusters = append(clusters, name)
		}
		sort.Strings(clusters)
		for _, name := range clusters {
			cc := RepMan.Confs[name]
			if cc.SecretKey == nil {
				cc.SecretKey = c.SecretKey // the instance key, loaded once above
			}
			if err := cc.RevealEffective(os.Stdout, name); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	},
}
