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
			var check func(io.Reader) error
			if cliStreamJob {
				check = jobStreamCheck(jobClient())
			}
			n, err := netstream.ReceiveChecked(cliStreamListen, os.Stdout, time.Duration(cliStreamAcceptTimeout)*time.Second, check)
			if err != nil {
				fmt.Fprintf(os.Stderr, "stream: %v (%d bytes received)\n", err, n)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "stream: %d bytes received on %s\n", n, cliStreamListen)
			return
		}
		var header []byte
		if cliStreamJob {
			c := jobClient()
			secret, _ := c.StreamSecret()
			var err error
			if header, err = jobapi.StreamHeader(secret, jobapi.StreamToMonitor, c.Name(), time.Now()); err != nil {
				fmt.Fprintf(os.Stderr, "stream: %v\n", err)
				os.Exit(1)
			}
		}
		n, err := netstream.Send(os.Stdin, cliStreamTo, netstream.Options{
			Header:           header,
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
	cmd.Flags().BoolVar(&cliStreamJob, "job", false, "A jobs stream: send the job stream header, or take only a stream with replication-manager's (identity as for job, --secret-env)")
	cmd.Flags().StringVar(&cliJobSecretEnv, "secret-env", "MYSQL_ROOT_PASSWORD", "Name of the environment variable holding the database password")
	cmd.MarkFlagRequired("to")
}

// jobStreamWindow is the accepted distance to a stream header's timestamp (the script's
// JOB_STREAM_WINDOW).
const jobStreamWindow = 300 * time.Second

// jobStreamCheck takes a stream only with the header replication-manager signs for this
// server, once (the nonces of the window in a file next to the other jobs files).
func jobStreamCheck(c *jobapi.Client) func(io.Reader) error {
	secret, _ := c.StreamSecret()
	return func(r io.Reader) error {
		line, err := jobapi.ReadStreamHeader(r)
		if err != nil {
			return err
		}
		now := time.Now()
		nonce, err := jobapi.CheckStreamHeader(line, secret, jobapi.StreamToDB, c.Name(), now, jobStreamWindow)
		if err != nil {
			return err
		}
		return jobapi.TakeStreamNonce(filepath.Join(os.TempDir(), "replication-manager-stream-nonces"), nonce, now, 2*jobStreamWindow)
	}
}
