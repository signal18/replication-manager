//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"fmt"
	"os"
	"time"

	"github.com/signal18/replication-manager/utils/netstream"
	"github.com/spf13/cobra"
)

var (
	cliStreamListen           string
	cliStreamAcceptTimeout    int
	cliStreamTo               string
	cliStreamTLS              bool
	cliStreamTLSSkipVerify    bool
	cliStreamGzip             bool
	cliStreamCompressionLevel int
	cliStreamConnectTimeout   int
)

// streamCmd sends its standard input to a TCP receiver, the `socat -u STDIN TCP:host:port`
// of the jobs scripts for images that ship no socat (PostgreSQL). Nothing is printed on
// stdout; the exit code is the outcome, the reason goes to stderr.
var streamCmd = &cobra.Command{
	Use:   "stream",
	Short: "Send stdin to a TCP receiver",
	Long:  `Send the standard input to host:port, optionally through TLS and parallel gzip. Used by the database jobs to stream a backup to replication-manager without socat.`,
	Run: func(cmd *cobra.Command, args []string) {
		if cliStreamListen != "" {
			// the receiving side: one connection, its bytes to stdout
			n, err := netstream.Receive(cliStreamListen, os.Stdout, time.Duration(cliStreamAcceptTimeout)*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "stream: %v (%d bytes received)\n", err, n)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "stream: %d bytes received on %s\n", n, cliStreamListen)
			return
		}
		n, err := netstream.Send(os.Stdin, cliStreamTo, netstream.Options{
			TLS:              cliStreamTLS,
			TLSSkipVerify:    cliStreamTLSSkipVerify,
			Gzip:             cliStreamGzip,
			CompressionLevel: cliStreamCompressionLevel,
			ConnectTimeout:   time.Duration(cliStreamConnectTimeout) * time.Second,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "stream: %v (%d bytes sent)\n", err, n)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "stream: %d bytes sent to %s\n", n, cliStreamTo)
	},
}

func initStreamFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliStreamTo, "to", "", "Receiver address host:port")
	cmd.Flags().StringVar(&cliStreamListen, "listen", "", "Receive instead: listen on host:port for one connection and write what it sends to stdout")
	cmd.Flags().IntVar(&cliStreamAcceptTimeout, "accept-timeout", 600, "Seconds to wait for the sender when listening")
	cmd.Flags().BoolVar(&cliStreamTLS, "tls", false, "Connect to the receiver through TLS")
	cmd.Flags().BoolVar(&cliStreamTLSSkipVerify, "tls-skip-verify", false, "Accept the receiver certificate unchecked")
	cmd.Flags().BoolVar(&cliStreamGzip, "gzip", false, "Compress the stream on the sender with parallel gzip")
	cmd.Flags().IntVar(&cliStreamCompressionLevel, "compression-level", 0, "Gzip level 1 to 9, 0 for the default")
	cmd.Flags().IntVar(&cliStreamConnectTimeout, "connect-timeout", 10, "Seconds to wait for the connection")
	cmd.MarkFlagRequired("to")
}
