//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/signal18/replication-manager/utils/jobapi"
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
	cliStreamJob              bool
)

// streamCmd sends its standard input to a TCP receiver (--to), or receives one stream to its
// standard output (--listen): the jobs scripts' data path, without socat. Nothing else is
// printed on stdout; the exit code is the outcome (0 done, 1 error, 3 the receiver closed the
// stream first, or every stream received was refused), the reason goes to stderr.
var streamCmd = &cobra.Command{
	Use:   "stream",
	Short: "Send stdin to a TCP receiver",
	Long:  `Send the standard input to host:port, optionally through TLS and parallel gzip. Used by the database jobs to stream a backup to replication-manager without socat.`,
	Run: func(cmd *cobra.Command, args []string) {
		if (cliStreamListen == "") == (cliStreamTo == "") {
			fmt.Fprintln(os.Stderr, "stream: one of --to or --listen is required")
			os.Exit(1)
		}
		if cliStreamListen != "" {
			// the receiving side: one connection, its bytes to stdout
			var check func(io.Reader) error
			if cliStreamJob {
				check = jobStreamCheck(jobClient())
			}
			n, err := netstream.ReceiveChecked(cliStreamListen, os.Stdout, time.Duration(cliStreamAcceptTimeout)*time.Second, check)
			if err != nil {
				fmt.Fprintf(os.Stderr, "stream: %v (%d bytes received)\n", err, n)
				if errors.Is(err, netstream.ErrRefused) {
					os.Exit(3)
				}
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "stream: %d bytes received on %s\n", n, cliStreamListen)
			return
		}
		var c *jobapi.Client
		if cliStreamJob {
			c = jobClient()
		}
		jobSend(c, os.Stdin, cliStreamTo, netstream.Options{
			TLS:              cliStreamTLS,
			TLSSkipVerify:    cliStreamTLSSkipVerify,
			Gzip:             cliStreamGzip,
			CompressionLevel: cliStreamCompressionLevel,
			ConnectTimeout:   time.Duration(cliStreamConnectTimeout) * time.Second,
		})
	},
}

// jobSend sends in to addr, after the job stream header for this server (StreamToMonitor)
// when c is set, and exits 1 on an error, 3 when the receiver closed the stream first.
func jobSend(c *jobapi.Client, in io.Reader, addr string, opts netstream.Options) {
	if c != nil {
		secret, _ := c.StreamSecret()
		var err error
		if opts.Header, err = jobapi.StreamHeader(secret, jobapi.StreamToMonitor, c.Name(), time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "stream: %v\n", err)
			os.Exit(1)
		}
	}
	n, err := netstream.Send(in, addr, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream: %v (%d bytes sent)\n", err, n)
		if errors.Is(err, netstream.ErrReceiverClosed) {
			os.Exit(3)
		}
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "stream: %d bytes sent to %s\n", n, addr)
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
	cmd.Flags().BoolVar(&cliStreamJob, "job", false, "A jobs stream: send the job stream header, or take only a stream with replication-manager's (identity as for job, --secret-env)")
	cmd.Flags().StringVar(&cliJobSecretEnv, "secret-env", "MYSQL_ROOT_PASSWORD", "Name of the environment variable holding the database password")
	cmd.Flags().StringVar(&cliJobURL, "url", "", "replication-manager URL, for --job (default $REPLICATION_MANAGER_URL)")
	cmd.Flags().StringVar(&cliJobCluster, "cluster", "", "Cluster name, for --job (default $REPLICATION_MANAGER_CLUSTER_NAME)")
	cmd.Flags().StringVar(&cliJobServer, "server", "", "Monitored server host, for --job (default $REPLICATION_MANAGER_HOST_NAME)")
	cmd.Flags().StringVar(&cliJobPort, "port", "", "Monitored server port, for --job (default $REPLICATION_MANAGER_HOST_PORT)")
}

// jobStreamCheck takes a stream only with the header replication-manager signs for this
// server, once (the nonces of the window in a file next to the other jobs files).
func jobStreamCheck(c *jobapi.Client) func(io.Reader) error {
	return func(r io.Reader) error {
		return c.CheckStream(r, filepath.Join(os.TempDir(), "replication-manager-stream-nonces"), time.Now())
	}
}
