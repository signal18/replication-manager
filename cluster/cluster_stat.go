// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"time"
)

// Logging Replication Delay Stat
type DelayStat struct {
	DelayAvg      float64 `json:"delay"`         // Average Seconds of Delay
	DelayCount    int32   `json:"delayCount"`    // Number of Delay Occurred
	SlaveErrCount int32   `json:"slaveErrCount"` // Number of Slave Err Occurred
	Counter       int32   `json:"counter"`       // Increment
}

type DelayStatHistory struct {
	DelayStat DelayStat `json:"delayStat"`
	Datetime  time.Time `json:"delayDT"`
}

type DelayHistoryList []DelayStatHistory

type ServerDelayStat struct {
	Total        DelayStat        `json:"total"` // Total Delay Average since SRM started
	Rotated      DelayStat        `json:"rotated"`
	Current      DelayStat        `json:"current"`
	CurrentDT    time.Time        `json:"currentDT"`
	DelayHistory DelayHistoryList `json:"delayHistory"`
}

// Get Current Datetime in hourly format
func (sds *ServerDelayStat) CurrentDelayDatetime(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func (sds *ServerDelayStat) ResetCurrentStat() {
	sds.Current = DelayStat{DelayAvg: 0, DelayCount: 0, SlaveErrCount: 0, Counter: 0}
	sds.CurrentDT = sds.CurrentDelayDatetime(time.Now())
}

func (sds *ServerDelayStat) ResetDelayStat() {
	sds.ResetCurrentStat()
	sds.Total = sds.Current
	sds.Rotated = sds.Current
	sds.DelayHistory = []DelayStatHistory{{sds.Current, sds.CurrentDT}}
}

func (sds *ServerDelayStat) UpdateCurrentDelayStat(s int64) {
	if s > 0 {
		sds.Current.DelayCount = sds.Current.DelayCount + 1
	}

	sds.Current.DelayAvg = ((sds.Current.DelayAvg*float64(sds.Current.Counter) + float64(s)) / (float64(sds.Current.Counter + 1)))
	sds.Current.Counter = sds.Current.Counter + 1
}

// Update Delay with Rotating Stats
func (sds *ServerDelayStat) UpdateDelayWithRotate(s int64, limit int) {
	sds.Rotated = sds.DelayHistory[0].DelayStat

	//Rotate Stat if exceed limit
	if len(sds.DelayHistory) >= limit {
		sds.DelayHistory = sds.DelayHistory[(len(sds.DelayHistory)-limit)+1:]
	}

	sds.ResetCurrentStat() //Reset Current Stat
	sds.UpdateCurrentDelayStat(s)
	sds.DelayHistory = append(sds.DelayHistory, DelayStatHistory{sds.Current, sds.CurrentDT})

	//Updating Total
	sds.Total.DelayAvg = (sds.Total.DelayAvg*float64(sds.Total.Counter) - (sds.Rotated.DelayAvg * float64(sds.Rotated.Counter)) + float64(s)) / float64(sds.Total.Counter+1-sds.Rotated.Counter)
	sds.Total.Counter = sds.Total.Counter + 1 - sds.Rotated.Counter

	if s > 0 {
		sds.Total.DelayCount = sds.Total.DelayCount + 1 - sds.Rotated.DelayCount
	} else {
		sds.Total.DelayCount = sds.Total.DelayCount - sds.Rotated.DelayCount
	}

}

// Update Delay without Rotating Stats
func (sds *ServerDelayStat) UpdateDelayWithoutRotate(s int64) {
	sds.UpdateCurrentDelayStat(s)
	sds.DelayHistory[len(sds.DelayHistory)-1].DelayStat = sds.Current

	//Updating Total
	sds.Total.DelayAvg = (sds.Total.DelayAvg*float64(sds.Total.Counter) + float64(s)) / float64(sds.Total.Counter+1)
	sds.Total.Counter = sds.Total.Counter + 1

	if s > 0 {
		sds.Total.DelayCount = sds.Total.DelayCount + 1
	}
}

func (sds *ServerDelayStat) UpdateDelayStat(s int64, limit int) {
	curTime := sds.CurrentDelayDatetime(time.Now())

	if curTime != sds.CurrentDT {
		sds.UpdateDelayWithRotate(s, limit)
	} else {
		sds.UpdateDelayWithoutRotate(s)
	}
}

func (sds *ServerDelayStat) UpdateTotalSlaveErrStat() {

}

func (sds *ServerDelayStat) UpdateSlaveErrorWithRotate(limit int) {
	sds.Rotated = sds.DelayHistory[0].DelayStat

	//Rotate Stat if reach limit
	if len(sds.DelayHistory) >= limit {
		sds.DelayHistory = sds.DelayHistory[(len(sds.DelayHistory)-limit)+1:]
	}
	sds.ResetCurrentStat() //Reset Current Stat

	sds.Current.SlaveErrCount = sds.Current.SlaveErrCount + 1
	sds.Current.Counter = sds.Current.Counter + 1
	sds.DelayHistory = append(sds.DelayHistory, DelayStatHistory{sds.Current, sds.CurrentDT})

	sds.Total.Counter = sds.Total.Counter + 1 - sds.Rotated.Counter
	sds.Total.SlaveErrCount = sds.Total.SlaveErrCount + 1 - sds.Rotated.SlaveErrCount
}

func (sds *ServerDelayStat) UpdateSlaveErrorWithoutRotate() {
	sds.Current.SlaveErrCount = sds.Current.SlaveErrCount + 1
	sds.Current.Counter = sds.Current.Counter + 1
	sds.DelayHistory[len(sds.DelayHistory)-1].DelayStat = sds.Current

	sds.Total.SlaveErrCount = sds.Total.SlaveErrCount + 1
	sds.Total.Counter = sds.Total.Counter + 1
}

func (sds *ServerDelayStat) UpdateSlaveErrorStat(limit int) {
	curTime := sds.CurrentDelayDatetime(time.Now())

	if curTime != sds.CurrentDT {
		sds.UpdateSlaveErrorWithRotate(limit)
	} else {
		sds.UpdateSlaveErrorWithoutRotate()
	}
}

func (cluster *Cluster) PrintTotalDelayStat() {
	var allStat map[string]DelayStat = make(map[string]DelayStat)
	for _, sl := range cluster.slaves {
		allStat[sl.URL] = sl.DelayStat.Total
	}

	jtext, err := json.MarshalIndent(allStat, " ", "\t")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Average delay for cluster %s : %s", cluster.Name, err)
		return
	}
	cluster.LogPrintf(LvlInfo, "Average delay for cluster %s : %s", cluster.Name, jtext)
}

func (cluster *Cluster) PrintDelayStatHistory() {
	var allStat map[string]DelayHistoryList = make(map[string]DelayHistoryList)
	for _, sl := range cluster.slaves {
		allStat[sl.URL] = sl.DelayStat.DelayHistory
	}

	jtext, err := json.MarshalIndent(allStat, " ", "\t")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Delay history for cluster %s : %s", cluster.Name, err)
		return
	}
	cluster.LogPrintf(LvlInfo, "Delay history for cluster %s : %s", cluster.Name, jtext)
}

func (cluster *Cluster) PrintDelayStat() {
	if cluster.Conf.PrintDelayStat {
		if cluster.LastDelayStatPrint.IsZero() || !time.Now().Before(cluster.LastDelayStatPrint.Add(time.Minute*time.Duration(cluster.Conf.PrintDelayStatInterval))) {
			cluster.LastDelayStatPrint = time.Now()
			cluster.PrintTotalDelayStat()
			if cluster.Conf.PrintDelayStatHistory {
				cluster.PrintDelayStatHistory()
			}
		}
	}
}
