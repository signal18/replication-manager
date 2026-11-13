//go:build !clients
// +build !clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"os"

	"github.com/signal18/replication-manager/utils/etcwriter"
	"github.com/spf13/cobra"
)

// etcWriterUID and etcWriterGroups are the user the parent drops to and every group it keeps:
// the files they could swap are not written (etcwriter.Unsafe).
var (
	etcWriterUID    int
	etcWriterGroups []int
)

func init() {
	etcCredentialWriterCmd.Flags().IntVar(&etcWriterUID, "uid", -1, "the user replication-manager drops to")
	etcCredentialWriterCmd.Flags().IntSliceVar(&etcWriterGroups, "groups", nil, "every group replication-manager keeps once dropped")
	rootCmd.AddCommand(etcCredentialWriterCmd)
}

// etcCredentialWriterCmd is the /etc credential writer, started as root by replication-manager
// before it drops root (CREDENTIAL_STORES.md "The /etc writer"): it reads the requests on its
// stdin and answers on file descriptor 3. It writes only a server credential (the list is its
// own, never its parent's) already set in one of the files given as arguments. Never run by
// hand.
var etcCredentialWriterCmd = &cobra.Command{
	Use:    etcwriter.Command + " [files]",
	Short:  "Write a rotated server credential in the configuration files (internal)",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		answers := os.NewFile(3, "answers")
		if answers == nil {
			fmt.Fprintln(os.Stderr, "started without its answers descriptor: run by replication-manager only")
			os.Exit(2)
		}
		if etcWriterUID < 0 || len(etcWriterGroups) == 0 {
			fmt.Fprintln(os.Stderr, "started without --uid and --groups: run by replication-manager only")
			os.Exit(2)
		}
		if err := etcwriter.Serve(os.Stdin, answers, args, etcWriterUID, etcWriterGroups, isServerSecretKey); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}
