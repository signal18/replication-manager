// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) JobAnalyzeSQL(persistent bool) error {
	var err error
	var logs string
	server := cluster.master

	if server == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel as no leader ")
		return errors.New("Analyze tables cancel as no leader")
	}
	if !cluster.Conf.MonitorSchemaChange {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel no schema monitor in config")
		return errors.New("Analyze tables cancel no schema monitor in config")
	}
	if cluster.inAnalyzeTables {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel already running")
		return errors.New("Analyze tables cancel already running")
	}
	if cluster.master.Tables == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel no table list")
		return errors.New("Analyze tables cancel no table list")
	}
	cluster.inAnalyzeTables = true
	defer func() {
		cluster.inAnalyzeTables = false
	}()

	// Preserve the old behavior of analyze tables, which is writing to the binlog
	local := false
	for _, t := range cluster.master.Tables {
		//	for _, s := range cluster.slaves {
		logs, err = dbhelper.AnalyzeTable(server.Conn, server.DBVersion, t.TableSchema+"."+t.TableName, local, persistent, "ALL", "")
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get database variables %s %s", server.URL, err)

		//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Analyse table %s on %s", t, s.URL)
		//	}
	}
	return err
}

func (cluster *Cluster) JobAnalyzeSchema(schema, tablename string, persistent bool) error {
	var err error
	var logs string
	server := cluster.master

	if server == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel as no leader ")
		return errors.New("Analyze tables cancel as no leader")
	}
	if !cluster.Conf.MonitorSchemaChange {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel no schema monitor in config")
		return errors.New("Analyze tables cancel no schema monitor in config")
	}
	if cluster.inAnalyzeTables {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel already running")
		return errors.New("Analyze tables cancel already running")
	}
	if cluster.master.Tables == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel no table list")
		return errors.New("Analyze tables cancel no table list")
	}
	cluster.inAnalyzeTables = true
	defer func() {
		cluster.inAnalyzeTables = false
	}()

	// Preserve the old behavior of analyze tables, which is writing to the binlog
	local := false
	for _, t := range cluster.master.Tables {
		if t.TableSchema == schema && (tablename == "" || t.TableName == tablename) {
			logs, err = dbhelper.AnalyzeTable(server.Conn, server.DBVersion, t.TableSchema+"."+t.TableName, local, persistent, "ALL", "")
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get database variables %s %s", server.URL, err)
		}
	}
	return err
}

func (cluster *Cluster) JobsGetEntries() (config.JobEntries, error) {
	var t config.Task
	var entries = config.JobEntries{
		Header:  config.GetLabelsAsMap(t),
		Servers: make(map[string]config.ServerTaskList),
	}

	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		if !s.JobsHasEntries() {
			if err := s.JobsRefreshEntries(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Jobs refresh skipped for %s: %s", s.URL, err)
			}
		}
		entries.Servers[s.Id] = s.JobsGetEntries()
	}

	return entries, nil
}

func (cluster *Cluster) GetSlowLogTable() {
	// Skip if previous cycle is not finished yet
	if !cluster.IsGettingSlowLog {
		cluster.IsGettingSlowLog = true
		wg := new(sync.WaitGroup)
		defer func() {
			cluster.IsGettingSlowLog = false
		}()

		for _, s := range cluster.Servers {
			if s != nil {
				wg.Add(1)
				go func() {
					err := s.GetSlowLogTable(wg)
					if err != nil && !isNoConnPoolError(err) {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "%s", err)
					}
				}()
			}
		}

		wg.Wait()
	}
}

func (cluster *Cluster) JobMyLoaderParseMeta(dir string) (config.MyDumperMetaData, error) {
	dir = strings.TrimSuffix(dir, "/")
	if cluster.VersionsMap.Get("mydumper").GreaterEqual("0.14.1") {
		return cluster.JobParseMyDumperMetaNew(dir)
	} else {
		return cluster.JobParseMyDumperMetaOld(dir)
	}
}

func (cluster *Cluster) JobParseMyDumperMeta(meta *backupmgr.BackupMetadata) error {
	var m config.MyDumperMetaData
	var err error

	m, err = cluster.JobMyLoaderParseMeta(meta.Dest)
	if err != nil {
		return err
	}

	meta.BinLogGtid = m.BinLogUuid
	meta.BinLogFilePos = m.BinLogFilePos
	meta.BinLogFileName = m.BinLogFileName

	return nil
}

func (cluster *Cluster) JobParseMyDumperMetaNew(dir string) (config.MyDumperMetaData, error) {

	var m config.MyDumperMetaData

	meta := dir + "/metadata"
	file, err := os.Open(meta)
	if err != nil {
		return m, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	var binlogFile, position, gtidSet string

	reFile := regexp.MustCompile(`^File\s*=\s*(.*)`)
	rePos := regexp.MustCompile(`^Position\s*=\s*(\d+)`)
	reGTID := regexp.MustCompile(`^Executed_Gtid_Set\s*=\s*(.*)`)

	for scanner.Scan() {
		line := scanner.Text()

		if binlogFile == "" {
			if matches := reFile.FindStringSubmatch(line); matches != nil {
				binlogFile = matches[1]
			}
		}

		if position == "" {
			if matches := rePos.FindStringSubmatch(line); matches != nil {
				position = matches[1]
			}
		}

		if gtidSet == "" {
			if matches := reGTID.FindStringSubmatch(line); matches != nil {
				gtidSet = matches[1]
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading file:", err)
		return m, err
	}

	m.BinLogUuid = gtidSet
	m.BinLogFilePos, _ = strconv.ParseUint(position, 10, 64)
	m.BinLogFileName = binlogFile

	return m, nil
}

func (cluster *Cluster) JobParseMyDumperMetaOld(dir string) (config.MyDumperMetaData, error) {

	var m config.MyDumperMetaData
	buf := new(bytes.Buffer)

	// metadata file name.
	meta := dir + "/metadata"

	// open a file.
	MetaFd, err := os.Open(meta)
	if err != nil {
		return m, err
	}
	defer MetaFd.Close()

	MetaRd := bufio.NewReader(MetaFd)
	for {
		line, err := MetaRd.ReadBytes('\n')
		if err != nil {
			break
		}

		if len(line) > 2 {
			newline := bytes.TrimLeft(line, "")
			buf.Write(bytes.Trim(newline, "\n"))
		}
		if strings.Contains(buf.String(), "Started") {
			splitbuf := strings.Split(buf.String(), ":")
			m.StartTimestamp, _ = time.ParseInLocation("2006-01-02 15:04:05", strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " "), time.Local)
		}
		if strings.Contains(buf.String(), "Log") {
			splitbuf := strings.Split(buf.String(), ":")
			m.BinLogFileName = strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " ")
		}
		if strings.Contains(buf.String(), "Pos") {
			splitbuf := strings.Split(buf.String(), ":")
			pos, _ := strconv.Atoi(strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " "))

			m.BinLogFilePos = uint64(pos)
		}

		if strings.Contains(buf.String(), "GTID") {
			splitbuf := strings.Split(buf.String(), ":")
			m.BinLogUuid = strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " ")
		}
		if strings.Contains(buf.String(), "Finished") {
			splitbuf := strings.Split(buf.String(), ":")
			m.EndTimestamp, _ = time.ParseInLocation("2006-01-02 15:04:05", strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " "), time.Local)
		}
		buf.Reset()

	}

	return m, nil
}

func (cluster *Cluster) SetJobsUpgradeSender(server *ServerMonitor) {
	cluster.SetState("WARN0148", state.State{ErrDesc: fmt.Sprintf(config.ClusterError["WARN0148"], server.URL), ErrType: config.LvlWarn, ErrFrom: "JOBS", ServerUrl: server.URL})
}

func (cluster *Cluster) CheckWaitRunJobSSH() {
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}

		if s.HasWaitRunJobSSHCookie() {
			s.DelWaitRunJobSSHCookie()
			go s.JobRunViaSSH()
		}
	}
}
