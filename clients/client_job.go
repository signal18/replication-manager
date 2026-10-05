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

	"github.com/signal18/replication-manager/utils/jobapi"
	"github.com/spf13/cobra"
)

var (
	cliJobURL       string
	cliJobCluster   string
	cliJobServer    string
	cliJobPort      string
	cliJobSecretEnv string
)

// jobCmd is the jobs script's side of the API jobs mode, for images without openssl and
// socat. The database password is read from an environment variable, never from a flag.
//
//	job needs TASK            exit 0 when replication-manager wants the task run now
//	job receiver TASK         prints the receiver address host:port opened for the task
//	job state TASK STATE      reports processing, done, error or waiting
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
	}
	if c.BaseURL == "" || c.Cluster == "" || c.Server == "" || c.Port == "" || c.Secret == "" {
		fmt.Fprintln(os.Stderr, "job: url, cluster, server, port and the secret environment variable are required")
		os.Exit(2)
	}
	return c
}

func jobFail(err error) {
	fmt.Fprintf(os.Stderr, "job: %v\n", err)
	os.Exit(2)
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
	Use:   "receiver TASK",
	Short: "Print the receiver address host:port opened for the task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addr, err := jobClient().Receiver(args[0])
		if err != nil {
			jobFail(err)
		}
		fmt.Println(addr)
	},
}

var jobStateCmd = &cobra.Command{
	Use:   "state TASK STATE",
	Short: "Report the task state: processing, done, error, waiting",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if err := jobClient().State(args[0], args[1]); err != nil {
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
	cmd.AddCommand(jobNeedsCmd, jobReceiverCmd, jobStateCmd)
}
