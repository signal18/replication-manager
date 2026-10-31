//go:build !clients
// +build !clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"os"

	"github.com/signal18/replication-manager/utils/s3fuse"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(backupFolderMountCmd)
}

// backupFolderMountCmd serves a cluster's backups folder mount for the replication-manager that
// started it (#2246): its parent writes the mount and the credentials on its stdin, it writes
// its events on file descriptor 3. Never run by hand.
var backupFolderMountCmd = &cobra.Command{
	Use:    s3fuse.ServerCommand,
	Short:  "Serve a backups folder S3 mount for its replication-manager (internal)",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		events := os.NewFile(3, "events")
		if events == nil {
			fmt.Fprintln(os.Stderr, "started without its events descriptor: run by replication-manager only")
			os.Exit(2)
		}
		if err := s3fuse.Serve(os.Stdin, events); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}
