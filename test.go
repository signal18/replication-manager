// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/regtest"
)

var (
	runtests         string
	showtests        bool
	teststopcluster  bool
	teststartcluster bool
)

func init() {
	rootCmd.AddCommand(testCmd)
	testCmd.Flags().StringVar(&runtests, "run-tests", "", "tests list to be run ")
	testCmd.Flags().BoolVar(&showtests, "show-tests", false, "tests list to be run ")
	testCmd.Flags().BoolVar(&teststartcluster, "test-start-cluster", true, "start the cluster between tests")
	testCmd.Flags().BoolVar(&teststopcluster, "test-stop-cluster", true, "start the cluster between tests")
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Perform regression test",
	Long:  `Perform named tests passed with argument --run-tests=test1,test2`,
	Run: func(cmd *cobra.Command, args []string) {

		currentCluster = new(cluster.Cluster)

		err := currentCluster.Init(confs[cfgGroup], cfgGroup, &tlog, 0, runUUID, Version, repmgrHostname, nil)
		currentCluster.SetLogStdout()
		currentCluster.SetTestStartCluster(teststartcluster)
		currentCluster.SetTestStopCluster(teststopcluster)
		go currentCluster.Run()
		if err != nil {
			log.WithError(err).Fatal("Error initializing cluster")
		}
		regtest := new(regtest.RegTest)
		if showtests == false {
			todotests := strings.Split(runtests, ",")
			for _, test := range todotests {
				regtest.RunAllTests(currentCluster, test)
			}
		}
		if showtests == true {
			log.Println(regtest.GetTests())
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		currentCluster.Close()
	},
}
