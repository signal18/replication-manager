//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/server"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	v3 "github.com/signal18/replication-manager/repmanv3"
)

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Print json informations",
	Long:  `To use for support issues`,
	Run: func(cmd *cobra.Command, args []string) {
		//cliClusters, err = cliGetClusters()
		cliInit(false)
		type Objects struct {
			Name     string
			Settings server.Settings         `json:"settings"`
			Servers  []cluster.ServerMonitor `json:"servers"`
			Master   cluster.ServerMonitor   `json:"master"`
			Slaves   []cluster.ServerMonitor `json:"slaves"`
			Crashes  []v3.Cluster_Crash      `json:"crashes"`
			Alerts   cluster.Alerts          `json:"alerts"`
		}
		type Report struct {
			Clusters []Objects `json:"clusters"`
		}
		var myReport Report

		client, err := v3.NewClient(context.Background(), v3Config)
		if err != nil {
			log.Fatal("Could not initialize v3 Client: %s", err)
		}

		for _, clusterName := range cliClusters {

			var myObjects Objects
			myObjects.Name = clusterName
			if strings.Contains(cliShowObjects, "settings") {
				c, err := client.GetCluster(context.Background(), &v3.Cluster{
					Name: clusterName,
				})
				if err != nil {
					log.Fatal("Error fetching GetCluster: %s", err)
				}

				// to get the Cluster response back into the Settings format we need to once
				// convert it to JSON and unmarshall it again :)
				buf, err := protojson.Marshal(c)
				// buf, err := json.Marshal(c)
				if err == nil {
					json.Unmarshal(buf, &myObjects.Settings)
				}
			}
			if strings.Contains(cliShowObjects, "servers") {
				srv, err := RetrieveServerMonitorsFromTopology(client, clusterName, v3.TopologyRetrieval_SERVERS)
				if err != nil {
					log.Fatalf("Could not retrieve ServerMonitors: %s", err)
				}
				myObjects.Servers = append(myObjects.Servers, srv...)
			}
			if strings.Contains(cliShowObjects, "master") {
				srv, err := RetrieveServerMonitorsFromTopology(client, clusterName, v3.TopologyRetrieval_MASTER)
				if err != nil {
					if !strings.Contains(err.Error(), v3.ErrClusterMasterNotSet.Error()) {
						log.Fatalf("Could not retrieve ServerMonitors: %s", err)
					}
				}
				if srv != nil {
					myObjects.Master = srv[0]
				}
			}
			if strings.Contains(cliShowObjects, "slaves") {
				srv, err := RetrieveServerMonitorsFromTopology(client, clusterName, v3.TopologyRetrieval_SLAVES)
				if err != nil {
					log.Fatalf("Could not retrieve ServerMonitors: %s", err)
				}
				myObjects.Slaves = append(myObjects.Slaves, srv...)
			}
			if strings.Contains(cliShowObjects, "crashes") {
				stream, err := client.RetrieveFromTopology(context.Background(),
					&v3.TopologyRetrieval{
						Cluster: &v3.Cluster{
							Name: clusterName,
						},
						Retrieve: v3.TopologyRetrieval_CRASHES,
					})

				if err != nil {
					log.Fatalf("Error fetching RetrieveFromTopology: %s", err)
				}

				for {
					st, err := stream.Recv()
					if err == io.EOF {
						break
					}

					if err != nil {
						log.Fatalf("Error retrieving topology: %s", err)
					}

					buf, err := st.MarshalJSON()
					if err != nil {
						log.Fatalf("Error marshalling servermonitor: %s", err)
					}

					var crash v3.Cluster_Crash
					err = protojson.Unmarshal(buf, &crash)
					if err != nil {
						log.Fatalf("Error unmarshalling servermonitor: %s", err)
					}

					myObjects.Crashes = append(myObjects.Crashes, crash)
				}
			}
			if strings.Contains(cliShowObjects, "alerts") {
				stream, err := client.RetrieveFromTopology(context.Background(),
					&v3.TopologyRetrieval{
						Cluster: &v3.Cluster{
							Name: clusterName,
						},
						Retrieve: v3.TopologyRetrieval_ALERTS,
					})

				if err != nil {
					log.Fatalf("Error fetching RetrieveFromTopology: %s", err)
				}

				for {
					st, err := stream.Recv()
					if err == io.EOF {
						break
					}

					if err != nil {
						log.Fatalf("Error retrieving topology: %s", err)
					}

					buf, err := st.MarshalJSON()
					if err != nil {
						log.Fatalf("Error marshalling servermonitor: %s", err)
					}

					var alerts cluster.Alerts
					err = json.Unmarshal(buf, &alerts)
					if err != nil {
						log.Fatalf("Error unmarshalling servermonitor: %s", err)
					}

					myObjects.Alerts = alerts
				}
			}
			myReport.Clusters = append(myReport.Clusters, myObjects)

		}
		data, err := json.MarshalIndent(myReport, "", "\t")
		if err != nil {
			fmt.Println(err)
			os.Exit(10)
		}

		fmt.Fprintf(os.Stdout, "%s\n", data)
	},
	PostRun: func(cmd *cobra.Command, args []string) {

	},
}

func RetrieveServerMonitorsFromTopology(client *v3.Client, clusterName string, retrieve v3.TopologyRetrieval_Retrieval) ([]cluster.ServerMonitor, error) {
	var list []cluster.ServerMonitor
	stream, err := client.RetrieveFromTopology(context.Background(),
		&v3.TopologyRetrieval{
			Cluster: &v3.Cluster{
				Name: clusterName,
			},
			Retrieve: retrieve,
		})

	if err != nil {
		return nil, fmt.Errorf("Error fetching RetrieveFromTopology: %s", err)
	}

	for {
		st, err := stream.Recv()
		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("Error retrieving topology: %s", err)
		}

		buf, err := st.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("Error marshalling servermonitor: %s", err)
		}

		var smBuf cluster.ServerMonitor
		err = json.Unmarshal(buf, &smBuf)
		if err != nil {
			return nil, fmt.Errorf("Error unmarshalling servermonitor: %s", err)
		}

		list = append(list, smBuf)
	}

	return list, nil
}
