// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"os"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/utils/river"
)

func (cluster *Cluster) initCDC(wcg *sync.WaitGroup) {
	defer wcg.Done()
	if cluster.Conf.CdcReplications != nil {
		for _, cdc := range cluster.Conf.CdcReplications {
			cluster.LogPrintf(LvlInfo, "Loading CDC named replication:%s", cdc.Name)
			if cluster.GetMaster() != nil {
				cluster.LogPrintf(LvlInfo, "CDC named replication:%s have master tyoe %s", cdc.Name, cdc.Type)
				if strings.ToLower(cdc.Type) == "kafka" {
					var cnf river.Config
					cnf.MyHost = cluster.GetMaster().URL
					cnf.MyUser = cluster.GetMaster().User
					cnf.MyPassword = cluster.GetMaster().Pass
					cnf.DumpServerID = cdc.ReplcationServerID
					if cluster.GetMaster().IsMariaDB() {
						cnf.MyFlavor = "mariadb"
					} else {
						cnf.MyFlavor = "mysql"
					}
					cnf.BatchMode = "KAFKA"
					cnf.KafkaBrokers = cdc.Hosts
					cnf.DumpPath = cluster.WorkingDir + "/" + cluster.Name + "/cdc_" + cdc.Name
					cnf.DumpExec = cluster.GetMysqlDumpPath()
					cnf.DumpInit = true
					cnf.BatchSize = 100000
					cnf.BatchTimeOut = 1
					cnf.DataDir = cnf.DumpPath
					cnf.HaveHttp = false

					for _, watch := range cdc.Watches {
						var sc river.SourceConfig
						sc.Schema = watch.Schema
						sc.Tables = watch.Tables

						cnf.Sources = append(cnf.Sources, sc)
					}
					for _, rule := range cdc.Rules {
						var r river.Rule
						r.KTopic = rule.KafkaTopic
						r.KPartitions = rule.KafkaPartitions
						r.MSchema = rule.MasterSchema
						r.MTable = rule.MasterTable
						cnf.Rules = append(cnf.Rules, &r)
					}
					if _, err := os.Stat(cnf.DumpPath); os.IsNotExist(err) {
						os.MkdirAll(cnf.DumpPath, os.ModePerm)
						cluster.LogPrintf(LvlInfo, "CDC creating directory %s", cnf.DumpPath)
					}
					cdc, err := river.NewRiver(&cnf)

					if err != nil {
						cluster.LogPrintf(LvlInfo, "CDC error creating river %s", err)
						return
					}
					go cdc.Run()
					cluster.LogPrintf(LvlInfo, "CDC end init")

				}
			}
		}
	}
}
