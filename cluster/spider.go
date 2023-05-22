// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"log"
	"strings"

	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) SpiderShardsDiscovery() {
	for _, s := range cluster.Servers {
		cluster.tlog.Add(fmt.Sprintf("INFO: Is Spider Monitor server %s ", s.URL))
		mon, err := dbhelper.GetSpiderMonitor(s.Conn)
		if err != nil {
			continue
		}
		if mon != "" {
			cluster.tlog.Add(fmt.Sprintf("INFO: Retriving Spider Shards Server %s ", s.URL))
			extraUrl, err := dbhelper.GetSpiderShardUrl(s.Conn)
			if err == nil {
				if extraUrl != "" {
					for j, url := range strings.Split(extraUrl, ",") {
						var err error
						srv, err := cluster.newServerMonitor(url, cluster.GetDbUser(), cluster.GetDbPass(), true, cluster.GetDomain())
						srv.SetState(stateShard)
						cluster.Servers = append(cluster.Servers, srv)
						if err != nil {
							log.Fatalf("ERROR: Could not open connection to Spider Shard server %s : %s", cluster.Servers[j].URL, err)
						}
						if cluster.Conf.Verbose {
							cluster.tlog.Add(fmt.Sprintf("[%s] DEBUG: New server created: %v", cluster.Name, cluster.Servers[j].URL))
						}
					}
				}
			}
		}
	}

}

func (cluster *Cluster) SpiderSetShardsRepl() {
	for k, s := range cluster.Servers {
		url := s.URL

		if cluster.Conf.Heartbeat {
			for _, s2 := range cluster.Servers {
				url2 := s2.URL
				if url2 != url {
					host, port := misc.SplitHostPort(url2)
					err := dbhelper.SetHeartbeatTable(cluster.Servers[k].Conn)
					if err != nil {
						cluster.LogPrintf(LvlWarn, "Can not set heartbeat table to %s", url)
						return
					}
					_, err = dbhelper.SetMultiSourceRepl(cluster.Servers[k].Conn, host, port, cluster.GetRplUser(), cluster.GetRplPass(), "")
					if err != nil {
						log.Fatalf("ERROR: Can not set heartbeat replication from %s to %s : %s", url, url2, err)
					}
				}
			}
		}
	}
}
