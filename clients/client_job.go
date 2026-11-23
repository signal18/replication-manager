//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/signal18/replication-manager/utils/jobapi"
	"github.com/signal18/replication-manager/utils/netstream"
	"github.com/spf13/cobra"
)

var (
	cliJobURL           string
	cliJobCluster       string
	cliJobServer        string
	cliJobPort          string
	cliJobSecretEnv     string
	cliJobRetries       int
	cliJobRestore       string
	cliJobListen        string
	cliJobAcceptTimeout int
)

// jobCmd is the jobs scripts' only way to replication-manager (dbjobs_new.sh, postgres_job.sh):
// every call signed with the cluster's jobs key (one-time job body, jobapi), every stream with
// the job stream header (stream --job). The database password is read from an environment
// variable, never from a flag. Exit codes: 0 done (or yes), 1 no (not wanted, not printed),
// 2 error, 3 a stream refused or ended by the other side.
//
//	job needs TASK              exit 0 when replication-manager wants the task run now
//	job receiver TASK [NAME]    prints the receiver address host:port opened for the task
//	job state TASK STATE        reports processing, done, error or waiting
//	job usage                   reports the resource usage JSON read on stdin
//	job config FILE             the init container's config fetch
//	job log TASK LEVEL          sends the lines read on stdin to the job log
//	job log-level TASK LEVEL    exit 0 when replication-manager prints the task at that level
//	job check FILE              sends the jobs script FILE to the jobs check receiver
//	job upgrade FILE            receives the current jobs script into FILE
//	job config-refresh          exit 0 when the running server's config must be read again
//	job config-receiver         opens the config check receivers, prints their JSON
//	job config-dummy FILE       receives the generated config archive into FILE
var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Jobs script calls to replication-manager (API jobs mode)",
}

func jobClient() *jobapi.Client {
	env := func(flag, name string) string {
		if flag != "" {
			return flag
		}
		return os.Getenv(name)
	}
	c := &jobapi.Client{
		BaseURL: env(cliJobURL, "REPLICATION_MANAGER_URL"),
		Cluster: env(cliJobCluster, "REPLICATION_MANAGER_CLUSTER_NAME"),
		Server:  env(cliJobServer, "REPLICATION_MANAGER_HOST_NAME"),
		Port:    env(cliJobPort, "REPLICATION_MANAGER_HOST_PORT"),
		Secret:  os.Getenv(cliJobSecretEnv),
		JobsKey: jobapi.JobsKeyFromEnv(),
		Version: FullVersion,
	}
	if c.BaseURL == "" || c.Cluster == "" || c.Server == "" || c.Port == "" || (c.Secret == "" && c.JobsKey == "") {
		fmt.Fprintln(os.Stderr, "job: url, cluster, server, port and the jobs key (or the secret environment variable) are required")
		os.Exit(2)
	}
	return c
}

func jobFail(err error) {
	fmt.Fprintf(os.Stderr, "job: %v\n", err)
	os.Exit(2)
}

// jobRetry runs call up to --retries times, 2 seconds apart, each with a new job body: a
// state or log line lost to a replication-manager restart is sent again.
func jobRetry(call func() error) error {
	if cliJobRetries > 10 {
		cliJobRetries = 10 // 20 s at most between the first and the last attempt
	}
	var err error
	for i := 0; i < cliJobRetries || i == 0; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		if err = call(); err == nil {
			return nil
		}
	}
	return err
}

// jobReceiveFile listens on --listen, runs ask (which makes replication-manager send), and
// writes the stream it takes (signed for this server, StreamToDB) to file, through file.part:
// exit 3 when every stream was refused, 2 on another error.
func jobReceiveFile(c *jobapi.Client, file string, ask func() error) {
	if cliJobListen == "" {
		jobFail(fmt.Errorf("--listen host:port is required"))
	}
	ln, err := net.Listen("tcp", cliJobListen)
	if err != nil {
		jobFail(err)
	}
	defer ln.Close()
	if err := ask(); err != nil {
		jobFail(err)
	}
	tmp := file + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		jobFail(err)
	}
	_, err = netstream.AcceptChecked(ln, f, time.Duration(cliJobAcceptTimeout)*time.Second, jobStreamCheck(c))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "job: %v\n", err)
		if errors.Is(err, netstream.ErrRefused) {
			os.Exit(3)
		}
		os.Exit(2)
	}
}

var jobNeedsCmd = &cobra.Command{
	Use:   "needs TASK",
	Short: "Exit 0 when the task is wanted, 1 when it is not, 2 on error",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ok, err := jobClient().Needs(args[0])
		if err != nil {
			jobFail(err)
		}
		if !ok {
			os.Exit(1)
		}
	},
}

var jobReceiverCmd = &cobra.Command{
	Use:   "receiver TASK [NAME]",
	Short: "Print the receiver address host:port opened for the task (NAME: the file it receives)",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		addr, err := jobClient().ReceiverNamed(args[0], name)
		if err != nil {
			jobFail(err)
		}
		fmt.Println(addr)
	},
}

var jobStateCmd = &cobra.Command{
	Use:   "state TASK STATE",
	Short: "Report the task state: processing, done, error, waiting (--restore: the metadata of a physical reseed)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c := jobClient()
		if err := jobRetry(func() error { return c.StateWith(args[0], args[1], json.RawMessage(cliJobRestore)) }); err != nil {
			jobFail(err)
		}
	},
}

var jobLogCmd = &cobra.Command{
	Use:   "log TASK LEVEL",
	Short: "Send the lines read on stdin to the task's job log, when replication-manager prints that level",
	Long: "Reads stdin (1 MiB at most) and sends it to write-log at LEVEL (ERROR, WARN, INFO, DEBUG), " +
		"after asking jobs-log-level: nothing is sent at a level replication-manager does not print. " +
		"A literal \\n in the text is a line break, as in the jobs scripts' log lines.",
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		text, err := io.ReadAll(io.LimitReader(os.Stdin, jobapi.MaxLogSize+1))
		if err != nil {
			jobFail(err)
		}
		if len(bytes.TrimSpace(text)) == 0 {
			return
		}
		c := jobClient()
		ok, err := c.LogLevel(args[0], args[1])
		if err != nil {
			jobFail(err)
		}
		if !ok {
			return
		}
		if err := jobRetry(func() error { return c.Log(args[0], args[1], string(text)) }); err != nil {
			jobFail(err)
		}
	},
}

var jobLogLevelCmd = &cobra.Command{
	Use:   "log-level TASK LEVEL",
	Short: "Exit 0 when replication-manager prints the task's lines at LEVEL, 1 when not, 2 on error",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ok, err := jobClient().LogLevel(args[0], args[1])
		if err != nil {
			jobFail(err)
		}
		if !ok {
			os.Exit(1)
		}
	},
}

var jobCheckCmd = &cobra.Command{
	Use:   "check FILE",
	Short: "Send the jobs script FILE to the jobs check receiver replication-manager opens (receive-jobs-check)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := jobClient()
		f, err := os.Open(args[0])
		if err != nil {
			jobFail(err)
		}
		defer f.Close()
		addr, err := c.JobsCheckReceiver()
		if err != nil {
			jobFail(err)
		}
		jobSend(c, f, addr, netstream.Options{})
	},
}

var jobUpgradeCmd = &cobra.Command{
	Use:   "upgrade FILE",
	Short: "Receive the current jobs script into FILE (send-jobs-upgrade): exit 0 received, 2 on error, 3 refused",
	Long: "Listens on --listen (the jobs listener, SST_RECEIVER_PORT), asks send-jobs-upgrade, and writes " +
		"the stream signed by replication-manager for this server to FILE. The caller checks the script.",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := jobClient()
		jobReceiveFile(c, args[0], c.RequestJobsUpgrade)
	},
}

var jobConfigRefreshCmd = &cobra.Command{
	Use:   "config-refresh",
	Short: "Exit 0 when replication-manager wants the running server's configuration read again, 1 when not, 2 on error",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		ok, err := jobClient().NeedConfigRefresh()
		if err != nil {
			jobFail(err)
		}
		if !ok {
			os.Exit(1)
		}
	},
}

var jobConfigReceiverCmd = &cobra.Command{
	Use:   "config-receiver",
	Short: "Open the receivers of the configuration check and print their JSON (config-receiver)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		js, err := jobClient().ConfigReceiver()
		if err != nil {
			jobFail(err)
		}
		fmt.Println(string(js))
	},
}

var jobConfigDummyCmd = &cobra.Command{
	Use:   "config-dummy FILE",
	Short: "Receive the generated configuration archive into FILE (config-dummy-sender): exit 0 received, 2 on error, 3 refused",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := jobClient()
		jobReceiveFile(c, args[0], func() error {
			port, err := c.ConfigDummySend()
			if err != nil {
				return err
			}
			if _, listen, _ := net.SplitHostPort(cliJobListen); listen != port {
				// the port is known only from this answer: the send is already asked for and
				// goes to a port nobody listens on (it fails on its side)
				return fmt.Errorf("replication-manager was asked to send the config and sends it to port %s, not to the listener on %s: check SST_RECEIVER_PORT against the server's sst port", port, cliJobListen)
			}
			return nil
		})
	},
}

var jobUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Report the service resource usage read on stdin as JSON (the dbu route)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		report, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<16))
		if err != nil {
			jobFail(err)
		}
		if err := jobClient().ReportUsage(bytes.TrimSpace(report)); err != nil {
			jobFail(err)
		}
	},
}

var cliJobConfigForce bool

var jobConfigCmd = &cobra.Command{
	Use:   "config FILE",
	Short: "Download the server's config archive with the jobs login: exit 0 written, 1 not wanted now (never with --force), 2 on error",
	Long: "The init container's fetch: asks need-config-fetch, logs in with the cluster's jobs key " +
		"($JOBS_API_KEY or /credentials/JOBS_API_KEY, a token scoped to this server) and writes " +
		"config.tar.gz to FILE. No admin password. --force does not ask need-config-fetch: for a volume " +
		"that has no configuration yet, whose first install must happen whatever the route answers.",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := jobClient()
		tmp := args[0] + ".part"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			jobFail(err)
		}
		err = c.Config(f, cliJobConfigForce)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(tmp)
			if errors.Is(err, jobapi.ErrNoConfigFetch) {
				os.Exit(1)
			}
			jobFail(err)
		}
		if err := os.Rename(tmp, args[0]); err != nil {
			jobFail(err)
		}
	},
}

func initJobFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&cliJobURL, "url", "", "replication-manager URL (default $REPLICATION_MANAGER_URL)")
	cmd.PersistentFlags().StringVar(&cliJobCluster, "cluster", "", "Cluster name (default $REPLICATION_MANAGER_CLUSTER_NAME)")
	cmd.PersistentFlags().StringVar(&cliJobServer, "server", "", "Monitored server host (default $REPLICATION_MANAGER_HOST_NAME)")
	cmd.PersistentFlags().StringVar(&cliJobPort, "port", "", "Monitored server port (default $REPLICATION_MANAGER_HOST_PORT)")
	cmd.PersistentFlags().StringVar(&cliJobSecretEnv, "secret-env", "MYSQL_ROOT_PASSWORD", "Name of the environment variable holding the database password")
	cmd.PersistentFlags().IntVar(&cliJobRetries, "retries", 1, "Attempts of a state or log call, 2 seconds apart")
	jobStateCmd.Flags().StringVar(&cliJobRestore, "restore", "", "Restore metadata (JSON) of a physical reseed or flashback, sent with its state")
	jobConfigCmd.Flags().BoolVar(&cliJobConfigForce, "force", false, "Fetch without asking need-config-fetch (a volume with no configuration yet)")
	for _, c := range []*cobra.Command{jobUpgradeCmd, jobConfigDummyCmd} {
		c.Flags().StringVar(&cliJobListen, "listen", "", "Jobs listener address host:port (SST_RECEIVER_PORT)")
		c.Flags().IntVar(&cliJobAcceptTimeout, "accept-timeout", 300, "Seconds to wait for replication-manager's stream")
	}
	cmd.AddCommand(jobNeedsCmd, jobReceiverCmd, jobStateCmd, jobUsageCmd, jobConfigCmd, jobLogCmd, jobLogLevelCmd,
		jobCheckCmd, jobUpgradeCmd, jobConfigRefreshCmd, jobConfigReceiverCmd, jobConfigDummyCmd)
}
