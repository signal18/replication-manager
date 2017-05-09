// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import "github.com/tanji/replication-manager/gtid"

// Crash will store informations on a crash based on the replication stream
type Crash struct {
	URL                         string
	FailoverMasterLogFile       string
	FailoverMasterLogPos        string
	FailoverSemiSyncSlaveStatus bool
	FailoverIOGtid              *gtid.List
	ElectedMasterURL            string
}

type crashList []*Crash

func (cluster *Cluster) newCrash(*Crash) (*Crash, error) {
	crash := new(Crash)
	return crash, nil
}

func (cluster *Cluster) getCrash(URL string) *Crash {
	for _, cr := range cluster.crashes {
		cr.URL = URL
		return cr
	}
	return nil
}

// GetCrashes return crashes
func (cluster *Cluster) GetCrashes() crashList {
	return cluster.crashes
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
