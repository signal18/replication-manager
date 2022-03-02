//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster"
	"github.com/spf13/cobra"
)

var regTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Perform regression test",
	Long:  `Perform named tests passed with argument --run-tests=test1,test2`,
	Run: func(cmd *cobra.Command, args []string) {

		if cliTestConvert {

			type TestResults struct {
				Results []cluster.Test `json:"results"`
			}
			var cltests TestResults
			file, err := ioutil.ReadFile(cliTestConvertFile)
			if err != nil {
				fmt.Printf("File error: %v\n", err)
				return
			}
			err = json.Unmarshal(file, &cltests)
			if err != nil {
				fmt.Printf("File error: %v\n", err)
				return
			}
			var tmplgreen = "<tr><td>%s</td><td bgcolor=\"#adebad\">%s</td></tr>"
			var tmplred = "<tr><td>%s</td><td  bgcolor=\"##ff8080\">%s</td></tr>"
			fmt.Printf("<table>")
			for _, v := range cltests.Results {
				if v.Result == "FAIL" {
					fmt.Printf(tmplred, v.Name, v.Result)
				} else {
					fmt.Printf(tmplgreen, v.Name, v.Result)
				}
			}
			fmt.Printf("</table>")
			return
		}
		cliInit(true)
		//cliGetTopology()

		if cliTestShowTests == true {
			cliMonitor, _ = cliGetMonitor()
			log.Println(cliMonitor.Tests)
		}
		if cliTestShowTests == false {

			todotests := strings.Split(cliTTestRun, ",")

			for _, test := range todotests {
				var thistest cluster.Test
				thistest.Result = "TIMEOUT"
				thistest.Name = test
				data, _ := json.MarshalIndent(thistest, "", "\t")
				urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/tests/actions/run/" + test

				var startcluster RequetParam
				var stopcluster RequetParam
				var params []RequetParam

				startcluster.key = "provision"
				startcluster.value = strconv.FormatBool(cliTeststartcluster)
				params = append(params, startcluster)
				stopcluster.key = "unprovision"
				stopcluster.value = strconv.FormatBool(cliTeststopcluster)
				params = append(params, stopcluster)

				res, err := cliAPICmd(urlpost, params)
				if err != nil {
					fmt.Printf(string(data))
					log.Fatal("Error in API call")
				} else {
					if res != "" {
						fmt.Printf(res)

						err = json.Unmarshal([]byte(res), &thistest)
						if err != nil {
							fmt.Printf("No valid json in test result: %v\n", err)
							return
						}
						// post result in database
						if cliTestResultDBServer != "" {
							params := fmt.Sprintf("?timeout=2s")
							dsn := cliTestResultDBCredential + "@"
							dsn += "tcp(" + cliTestResultDBServer + ")/" + params
							c, err := sqlx.Open("mysql", dsn)
							if err != nil {
								fmt.Printf("Could not connect to result database %s", err)
							}
							err = c.Ping()
							if err != nil {
								fmt.Printf("Could not connect to result database %s", err)
							}
							_, err = c.Query("REPLACE INTO result.tests (version,test,path,result) VALUES('" + FullVersion + "','" + thistest.Name + "','" + thistest.ConfigFile + "','" + thistest.Result + "')")
							if err != nil {
								fmt.Printf("Could play sql to result database %s", err)
							}

							c.Close()
						}

					} else {
						fmt.Printf(string(data))
					}
				}
			}
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
	},
}
