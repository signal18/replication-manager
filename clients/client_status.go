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
	"fmt"
	"io"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	v3 "github.com/signal18/replication-manager/repmanv3"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Request status ",
	Long:  `The status command is used to request monitor daemon or pecific cluster status`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(false)

		client, err := v3.NewClient(context.Background(), v3Config)
		if err != nil {
			log.Fatal("Could not initialize v3 Client: %s", err)
		}

		c := &v3.Cluster{}

		if cfgGroup != "" {
			c.Name = cliClusters[cliClusterIndex]
		}

		res, err := client.ClusterStatus(context.Background(), c)

		if err != nil {
			log.Fatal("Error fetching ClusterStatus: %s", err)
		}

		if res.Alive == v3.ServiceStatus_ERRORS {
			stream, err := client.RetrieveAlerts(context.Background(), c)

			if err != nil {
				log.Fatal("Error fetching RetrieveAlerts: %s", err)
			}

			for {
				recv, err := stream.Recv()
				if err == io.EOF {
					break
				}

				if err != nil {
					log.Fatalf("Error receiving stream: %s", err)
				}

				buf, err := protojson.Marshal(recv)
				if err != nil {
					log.Fatalf("Could not marshal received message: %s", err)
				}

				fmt.Printf("%s\n", buf)
			}
		} else {
			fmt.Println(res.Alive)
		}
	},
}
