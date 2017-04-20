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
