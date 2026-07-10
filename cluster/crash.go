// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/gtid"
)

// Crash will store informations on a crash based on the replication stream
// swagger:response crash
type Crash struct {
	URL                         string
	FailoverMasterLogFile       string
	FailoverMasterLogPos        string
	NewMasterLogFile            string
	NewMasterLogPos             string
	FailoverSemiSyncSlaveStatus bool
	FailoverIOGtid              *gtid.List
	// FailoverIOGtidString is the election GTID in its usable text form
	// ("domain-server-seq"), the anchor for logical recovery of the diverged
	// old master: SET GLOBAL gtid_slave_pos = "<this>" then CHANGE MASTER …
	// master_use_gtid=slave_pos. Backfilled from FailoverIOGtid on read so it
	// survives clusterstate.json reloads.
	FailoverIOGtidString string `json:"failoverIOGtidString"`
	ElectedMasterURL     string
	UnixTimestamp               int64
	Switchover                  bool
	// Lost-events delta: captured from the diverged old master at rejoin and
	// analyzed (srv_lostevents.go). The verdict decides the recovery path.
	DeltaArchive          string `json:"deltaArchive"`
	DeltaDecoded          string `json:"deltaDecoded"`
	DeltaFlashbackDecoded string `json:"deltaFlashbackDecoded"`
	DeltaAnalyzed         bool   `json:"deltaAnalyzed"`
	DeltaFlashable        bool   `json:"deltaFlashable"`
	DeltaTransactions     int    `json:"deltaTransactions"`
	DeltaRowEvents        int    `json:"deltaRowEvents"`
	DeltaDDL              int    `json:"deltaDdl"`
	DeltaStatementDML     int    `json:"deltaStatementDml"`
}

// Collection of Crash reports
// swagger:response crashList
type crashList []*Crash

func (clist *crashList) Purge(keep int) {
	if keep <= 0 {
		*clist = nil
		return
	}
	if len(*clist) > keep {
		*clist = (*clist)[len(*clist)-keep:]
	}
}

func (clist *crashList) StoreLastN(cr *Crash, keep int) {
	*clist = append(*clist, cr)
	clist.Purge(keep)
}

func (clist *crashList) GetLatest() *Crash {
	size := len(*clist)
	if size < 1 {
		return nil
	}

	return (*clist)[size-1]
}

func (cluster *Cluster) newCrash(*Crash) (*Crash, error) {
	crash := new(Crash)
	crash.UnixTimestamp = time.Now().Unix()
	return crash, nil
}

func (cluster *Cluster) getCrashFromJoiner(URL string) *Crash {
	for _, cr := range cluster.Crashes {
		if cr.URL == URL {
			return cr
		}
	}
	return nil
}

func (cluster *Cluster) getCrashFromMaster(URL string) *Crash {
	for _, cr := range cluster.Crashes {
		if cr.ElectedMasterURL == URL {
			return cr
		}
	}
	return nil
}

// GetCrashes return crashes, with the election GTID backfilled into its
// usable text form so the logical recovery anchor is directly readable.
func (cluster *Cluster) GetCrashes() crashList {
	for _, cr := range cluster.Crashes {
		if cr != nil && cr.FailoverIOGtidString == "" && cr.FailoverIOGtid != nil {
			cr.FailoverIOGtidString = cr.FailoverIOGtid.Sprint()
		}
	}
	return cluster.Crashes
}

func (crash *Crash) delete(cl *crashList) {
	lsm := *cl
	for k, s := range lsm {
		if crash.URL == s.URL {
			lsm[k] = lsm[len(lsm)-1]
			lsm[len(lsm)-1] = nil
			lsm = lsm[:len(lsm)-1]
			break
		}
	}
	*cl = lsm
}

func (crash *Crash) Save(path string) error {
	saveJson, _ := json.MarshalIndent(crash, "", "\t")
	err := os.WriteFile(path, saveJson, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (crash *Crash) Purge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := os.ReadDir(path)
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
