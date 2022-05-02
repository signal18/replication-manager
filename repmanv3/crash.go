// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package repmanv3

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"strings"

	"github.com/signal18/replication-manager/utils/gtid"
)

// Cluster_Crash will store informations on a Cluster_Crash based on the replication stream
// swagger:response Cluster_Crash
// type Cluster_Crash struct {
// 	URL                         string
// 	FailoverMasterLogFile       string
// 	FailoverMasterLogPos        string
// 	NewMasterLogFile            string
// 	NewMasterLogPos             string
// 	FailoverSemiSyncSlaveStatus bool
// 	FailoverIOGtid              *gtid.List
// 	ElectedMasterURL            string
// }

// Collection of Cluster_Crash reports
type CrashList []*Cluster_Crash

func (crash *Cluster_Crash) GetFailoverIOGtid() (l gtid.List) {
	for _, g := range crash.FailoverIoGtids {
		l = append(l, *g)
	}
	return l
}

func (crash *Cluster_Crash) SetFailoverIOGtid(l gtid.List) {
	for _, g := range l {
		crash.FailoverIoGtids = append(crash.FailoverIoGtids, &g)
	}
}

func (crash *Cluster_Crash) delete(cl *CrashList) {
	lsm := *cl
	for k, s := range lsm {
		if crash.Url == s.Url {
			lsm[k] = lsm[len(lsm)-1]
			lsm[len(lsm)-1] = nil
			lsm = lsm[:len(lsm)-1]
			break
		}
	}
	*cl = lsm
}

func (crash *Cluster_Crash) Save(path string) error {
	saveJson, _ := json.MarshalIndent(crash, "", "\t")
	err := ioutil.WriteFile(path, saveJson, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (crash *Cluster_Crash) Purge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	i := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "failover") {
			i++
			drop[file.Name()] = i
		}
	}
	for key, value := range drop {

		if value < len(drop)-keep {
			os.Remove(path + "/" + key)
		}

	}
	return nil
}
