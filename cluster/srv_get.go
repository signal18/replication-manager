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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func (server *ServerMonitor) GetProcessList() []dbhelper.Processlist {
	return server.FullProcessList
}

func (server *ServerMonitor) GetSshEnv() string {
	/*
		REPLICATION_MANAGER_USER
		REPLICATION_MANAGER_PASSWORD
		REPLICATION_MANAGER_URL
		REPLICATION_MANAGER_URL_HOST
		REPLICATION_MANAGER_URL_PORT
		REPLICATION_MANAGER_CLUSTER_NAME
		REPLICATION_MANAGER_HOST_NAME
		REPLICATION_MANAGER_HOST_USER
		REPLICATION_MANAGER_HOST_PASSWORD
		REPLICATION_MANAGER_HOST_PORT

	*/
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}

	// shellEsc escapes single quotes for safe embedding in shell single-quoted strings.
	// Using single quotes avoids issues with double quotes, backslashes, and dollar signs
	// in passwords and other values.
	shellEsc := func(s string) string {
		return strings.ReplaceAll(s, "'", "'\"'\"'")
	}

	// Build shell exports using single-quoted values to prevent injection from
	// passwords containing double quotes, backslashes, or dollar signs.
	shellExport := func(name, value string) string {
		return "export " + name + "='" + shellEsc(value) + "'"
	}

	env := shellExport("REPLICATION_MANAGER_HOST_USER", server.User) +
		";" + shellExport("REPLICATION_MANAGER_HOST_PASSWORD", server.Pass) +
		";" + shellExport("MYSQL_ROOT_PASSWORD", server.Pass) +
		";" + shellExport("REPLICATION_MANAGER_URL", "https://"+server.ClusterGroup.Conf.MonitorAddress+":"+server.ClusterGroup.Conf.APIPort) +
		";" + shellExport("REPLICATION_MANAGER_URL_HOST", server.ClusterGroup.Conf.MonitorAddress) +
		";" + shellExport("REPLICATION_MANAGER_URL_PORT", server.ClusterGroup.Conf.APIPort) +
		";" + shellExport("REPLICATION_MANAGER_USER", adminuser) +
		";" + shellExport("REPLICATION_MANAGER_PASSWORD", adminpassword) +
		";" + shellExport("REPLICATION_MANAGER_HOST_NAME", server.Host) +
		";" + shellExport("REPLICATION_MANAGER_HOST_PORT", server.Port) +
		";" + shellExport("REPLICATION_MANAGER_CLUSTER_NAME", server.ClusterGroup.Name) +
		";" + shellExport("REPLICATION_MANAGER_JOBS_MODE", server.ClusterGroup.Conf.SchedulerJobsMode)

	// Export the Docker image tag so upgrade scripts can parse the target version
	if server.ClusterGroup.Conf.ProvDbImg != "" {
		env += ";" + shellExport("REPLICATION_MANAGER_DB_DOCKER_IMG", server.ClusterGroup.Conf.ProvDbImg)
	}

	// Export resolved OS family info from db_distributions.json (tag-filtered)
	if osf := server.ClusterGroup.Configurator.GetActiveDBOsFamily(); osf != nil {
		env += ";" + shellExport("REPLICATION_MANAGER_DB_REPO_TYPE", osf.RepoType)
		if osf.RepoBaseURL != "" {
			env += ";" + shellExport("REPLICATION_MANAGER_DB_REPO_BASE_URL", osf.RepoBaseURL)
		}
		if osf.RepoKeyURL != "" {
			env += ";" + shellExport("REPLICATION_MANAGER_DB_REPO_KEY_URL", osf.RepoKeyURL)
		}
	}

	// Export resolved deploy method from db_distributions.json (tag-filtered)
	if dm := server.ClusterGroup.Configurator.GetActiveDBDeployMethod(); dm != nil {
		env += ";" + shellExport("REPLICATION_MANAGER_DB_DEPLOY_METHOD", dm.Type)
	}

	return env + "\n"
}

func (server *ServerMonitor) GetUniversalGtidServerID() uint64 {
	cluster := server.ClusterGroup
	if server.IsMariaDB() {
		return uint64(server.ServerID)
	}
	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", " %s %s", server.Variables.Get("SERVER_UUID"), server.URL)
		return crc64.Checksum([]byte(strings.ToUpper(server.Variables.Get("SERVER_UUID"))), server.GetCluster().GetCrcTable())

	}
	return 0
}

func (server *ServerMonitor) GetSourceClusterName() string {
	return server.SourceClusterName
}

func (server *ServerMonitor) GetProcessListReplicationLongQuery() string {
	if !server.ClusterGroup.Conf.MonitorProcessList {
		return ""
	}
	for _, q := range server.FullProcessList {
		if strings.HasPrefix(q.Command, "Slave_worker") && q.State.Valid && !strings.HasPrefix(q.State.String, "Waiting") {
			if q.Time.Valid && server.ClusterGroup.Conf.FailMaxDelay != -1 && q.Time.Float64 > float64(server.ClusterGroup.Conf.FailMaxDelay) {
				if q.Info.Valid {
					return q.Info.String
				}
			}
		}
	}
	return ""
}

func (server *ServerMonitor) GetSchemas() ([]string, string, error) {
	if server.Conn == nil {
		return nil, "", errors.New("No available connection")
	}
	return dbhelper.GetSchemas(server.Conn)
}

func (server *ServerMonitor) GetPrometheusMetrics() string {
	metrics := server.GetDatabaseMetrics()
	var s string
	for _, m := range metrics {
		v := strings.Split(m.Name, ".")
		if v[2] == "pfs" {
			s = s + v[2] + "_" + v[3] + "{instance=\"" + v[1] + "\"} " + m.Value + "\n"
		} else {
			s = s + v[2] + "{instance=\"" + v[1] + "\"} " + m.Value + "\n"
		}
	}
	return s
}

func (server *ServerMonitor) GetReplicationServerID() uint64 {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	return ss.MasterServerID
}

func (server *ServerMonitor) GetReplicationDelay() int64 {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	if !ss.SecondsBehindMaster.Valid {
		return 0
	}
	return ss.SecondsBehindMaster.Int64
}

func (server *ServerMonitor) GetReplicationHearbeatPeriod() float64 {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	return ss.SlaveHeartbeatPeriod
}

func (server *ServerMonitor) GetReplicationUsingGtid() string {
	if server.IsMariaDB() {
		ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
		if sserr != nil {
			return "No"
		}
		return ss.UsingGtid.String
	} else {
		if server.HaveMySQLGTID {
			return "Yes"
		}
		return "No"
	}
}

func (server *ServerMonitor) GetBindAddress() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.Host
	}

	if server.ClusterGroup.Conf.DbServersBindAddress != "" {
		return server.ClusterGroup.Conf.DbServersBindAddress
	}

	if server.ClusterGroup.Conf.ProvUseIpv6 {
		return "[::]"
	}

	return "0.0.0.0"
}

func (server *ServerMonitor) IsReplicationUsingGtidStrict() bool {
	if server.IsMariaDB() {
		if server.Variables.Get("GTID_STRICT_MODE") == "ON" {
			return true
		} else {
			return false
		}
	} else {
		return true
	}
}

func (server *ServerMonitor) GetReplicationMasterHost() string {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return ""
	}
	return ss.MasterHost.String
}

func (server *ServerMonitor) GetReplicationMasterPort() string {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return "3306"
	}
	return ss.MasterPort.String
}

func (server *ServerMonitor) GetSibling() *ServerMonitor {
	ssserver, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return nil
	}
	for _, sl := range server.ClusterGroup.Servers {
		sssib, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err != nil {
			continue
		}
		if sssib.MasterServerID == ssserver.MasterServerID && sl.ServerID != server.ServerID {
			return sl
		}
	}
	return nil
}

func (server *ServerMonitor) GetSlaveStatus(name string) (*dbhelper.SlaveStatus, error) {
	if server.Replications != nil {
		for _, ss := range server.Replications {
			if ss.ConnectionName.String == name {
				return &ss, nil
			}
		}
	}
	return nil, errors.New("Empty replications channels")
}

func (server *ServerMonitor) GetSlaveStatusLastSeen(name string) (*dbhelper.SlaveStatus, error) {
	if server.LastSeenReplications != nil {
		for _, ss := range server.LastSeenReplications {
			if ss.ConnectionName.String == name {
				return &ss, nil
			}
		}
	} else {
		return server.GetSlaveStatus(name)
	}
	return nil, errors.New("Empty replications channels")
}

func (server *ServerMonitor) GetAllSlavesStatus() []dbhelper.SlaveStatus {
	return server.Replications
}

func (server *ServerMonitor) GetMasterStatus() dbhelper.MasterStatus {
	return server.MasterStatus
}

func (server *ServerMonitor) GetLastPseudoGTID() (string, string, error) {
	return dbhelper.GetLastPseudoGTID(server.Conn)
}

func (server *ServerMonitor) GetBinlogPosFromPseudoGTID(GTID string) (string, string, string, error) {
	return dbhelper.GetBinlogEventPseudoGTID(server.Conn, GTID, server.BinaryLogFile)
}

func (server *ServerMonitor) GetBinlogPosAfterSkipNumberOfEvents(file string, pos string, skip int) (string, string, string, error) {
	return dbhelper.GetBinlogPosAfterSkipNumberOfEvents(server.Conn, file, pos, skip)
}

func (server *ServerMonitor) GetNumberOfEventsAfterPos(file string, pos string) (int, string, error) {
	return dbhelper.GetNumberOfEventsAfterPos(server.Conn, file, pos)
}

func (server *ServerMonitor) GetTableFromDict(URI string) (*dbhelper.Table, error) {
	var emptyTable *dbhelper.Table = new(dbhelper.Table)
	val, ok := server.DictTables.CheckAndGet(URI)
	if !ok {
		if len(server.DictTables.ToNewMap()) == 0 {
			return emptyTable, errors.New("Empty")
		} else {
			return emptyTable, errors.New("Not found")
		}
	} else {
		return val, nil
	}
}

func (server *ServerMonitor) GetMetaDataLocks() []dbhelper.MetaDataLock {
	if server.MetaDataLocks == nil {
		return make([]dbhelper.MetaDataLock, 0)
	}

	return server.MetaDataLocks
}

func (server *ServerMonitor) GetQueryResponseTime() []dbhelper.ResponseTime {
	var qrt []dbhelper.ResponseTime
	logs := ""
	var err error
	qrt, logs, err = dbhelper.GetQueryResponseTime(server.Conn, server.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Can't fetch Query Response Time ")
	if qrt == nil {
		qrt = make([]dbhelper.ResponseTime, 0)
	}
	return qrt
}

func (server *ServerMonitor) GetVariables(diff bool) []config.VariableState {
	variables := server.VariablesMap.GetVariables(diff)
	misc.SortByKey(variables, func(v config.VariableState) string { return v.VariableName })
	return variables
}

func (server *ServerMonitor) GetVariablesCaseSensitive() map[string]string {
	variables, _, _ := dbhelper.GetVariablesCase(server.Conn, server.DBVersion, "LOWER")
	return variables
}

func (server *ServerMonitor) GetQueryFromPFSDigest(digest string) (string, string, error) {
	for _, v := range server.PFSQueries.ToNewMap() {
		if v.Digest == digest {
			// Prefer the concrete sample query (needed for EXPLAIN).
			// Fall back to the normalised digest_text if no sample was captured yet.
			q := v.Sample_query
			if q == "" {
				q = v.Query
			}
			return v.Schema_name, q, nil
		}
	}
	return "", "", errors.New("Query digest not found in PFS")
}

func (server *ServerMonitor) GetQueryFromSlowLogDigest(digest string) (string, string, error) {
	for _, v := range server.SlowLog.Buffer {
		if v.Digest == digest {
			return v.Db, v.Query, nil
		}
	}
	return "", "", errors.New("Query digest not found in PFS")
}

func (server *ServerMonitor) GetQueryExplain(schema string, query string) ([]dbhelper.Explain, error) {
	explainPlan, logs, err := dbhelper.GetQueryExplain(server.Conn, server.DBVersion, schema, query)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Can't get Explain %s %s ", server.URL, err)
	return explainPlan, err
}

func (server *ServerMonitor) GetQueryAnalyze(schema string, query string) (string, string, error) {
	return dbhelper.AnalyzeQuery(server.Conn, server.DBVersion, schema, query)
}

func (server *ServerMonitor) GetQueryExplainPFS(digest string) ([]dbhelper.Explain, error) {
	schema, query, err := server.GetQueryFromPFSDigest(digest)
	if err != nil {
		return nil, err
	}
	return server.GetQueryExplain(schema, query)
}

// GetCachedExplainPFS returns the persisted explain plan for a digest from the
// on-disk cache.  It never hits the live database — the plan was captured at
// snapshot time and is valid until schema or statistics change.
// Returns nil, error when the digest is not yet in the cache.
func (server *ServerMonitor) GetCachedExplainPFS(digest string) (*PFSExplainRecord, error) {
	server.PFSExplainCacheMu.Lock()
	rec, ok := server.PFSExplainCache[digest]
	server.PFSExplainCacheMu.Unlock()
	if ok {
		return &rec, nil
	}
	return nil, errors.New("digest not found in PFS explain cache")
}

// GetAllCachedExplainPFS returns every cached explain record for this server,
// sorted by digest text for stable output.
func (server *ServerMonitor) GetAllCachedExplainPFS() []PFSExplainRecord {
	server.PFSExplainCacheMu.Lock()
	recs := make([]PFSExplainRecord, 0, len(server.PFSExplainCache))
	for _, rec := range server.PFSExplainCache {
		recs = append(recs, rec)
	}
	server.PFSExplainCacheMu.Unlock()
	// Stable sort by digest text so API responses are deterministic.
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].DigestText < recs[j].DigestText
	})
	return recs
}

func (server *ServerMonitor) GetQueryAnalyzePFS(digest string) (string, string, error) {
	schema, query, err := server.GetQueryFromPFSDigest(digest)
	if err != nil {
		return "", "", err
	}
	return server.GetQueryAnalyze(schema, query)
}

func (server *ServerMonitor) GetQueryExplainSlowLog(digest string) ([]dbhelper.Explain, error) {
	schema, query, err := server.GetQueryFromSlowLogDigest(digest)
	if err != nil {
		return nil, err
	}
	return server.GetQueryExplain(schema, query)
}

func (server *ServerMonitor) GetQueryAnalyzeSlowLog(digest string) (string, string, error) {
	schema, query, err := server.GetQueryFromSlowLogDigest(digest)
	if err != nil {
		return "", "", err
	}
	return server.GetQueryAnalyze(schema, query)
}

func (server *ServerMonitor) GetStatus() []dbhelper.Variable {
	var status []dbhelper.Variable
	statf := func(key any, value any) bool {
		k := key.(string)
		v := value.(string)
		var r dbhelper.Variable
		r.Variable_name = k
		r.Value = v
		status = append(status, r)

		return true
	}
	server.Status.Callback(statf)
	misc.SortByKey(status, func(v dbhelper.Variable) string { return v.Variable_name })
	return status
}

func (server *ServerMonitor) GetServerConnections() int {
	res, _ := strconv.Atoi(server.Status.Get("THREADS_RUNNING"))
	return res
}

func (server *ServerMonitor) GetStatusDelta() []dbhelper.Variable {
	var delta []dbhelper.Variable
	deltaf := func(key any, value any) bool {
		k := key.(string)
		v := value.(string)
		//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status %s %s", k, v)
		i1, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			i2, err2 := strconv.ParseInt(server.PrevStatus.Get(k), 10, 64)
			//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status now %s %d", k, v)
			if err2 == nil && i2-i1 != 0 {
				//			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status prev %s %d", k, v)
				var r dbhelper.Variable
				r.Variable_name = k
				r.Value = strconv.FormatInt(i1-i2, 10)
				delta = append(delta, r)
			}
		}

		return true
	}
	server.Status.Callback(deltaf)
	misc.SortByKey(delta, func(v dbhelper.Variable) string { return v.Variable_name })
	return delta
}

func (server *ServerMonitor) GetErrorLog() *s18log.HttpLog {
	return &server.ErrorLog
}

func (server *ServerMonitor) GetSqlErrorLog() *s18log.HttpLog {
	return &server.SqlErrorLog
}

func (server *ServerMonitor) GetAuditLog() *s18log.HttpLog {
	return &server.AuditLog
}

func (server *ServerMonitor) GetPFSQueries() {
	// HavePFSSlowQueryLog gates the slow-query-log consumer feature only.
	// The PFS snapshot reads from events_statements_summary_by_digest which is
	// populated by the performance_schema itself — independent of whether the
	// slow query log consumers (events_statements_history_long /
	// events_stages_history) are enabled.  Blocking the snapshot on that flag
	// was causing FlushPFSSnapshotToLog to never fire even when
	// monitoring-performance-schema = true and performance_schema = ON.
	if !server.ClusterGroup.Conf.MonitorPFS || !server.HavePFS {
		return
	}
	if server.IsInPFSQueryCapture {
		return
	}
	server.IsInPFSQueryCapture = true
	defer func() { server.IsInPFSQueryCapture = false }()

	cluster := server.ClusterGroup

	// Determine if we must take a periodic snapshot.
	// MonitorPFSQueries enables the snapshot feature; MonitorPFSQueriesPeriod sets the
	// window in hours (default 1 hour when 0 is left unconfigured).
	periodHours := cluster.Conf.MonitorPFSQueriesPeriod
	if periodHours <= 0 {
		periodHours = 1
	}
	doSnapshot := cluster.Conf.MonitorPFSQueries &&
		time.Since(server.PFSLastSnapshot) >= time.Duration(periodHours)*time.Hour

	if doSnapshot {
		// Cancel any still-running explain goroutine from the previous period
		// before we overwrite PFSQueries with the new snapshot.
		if server.pfsExplainCancel != nil {
			server.pfsExplainCancel()
			server.pfsExplainCancel = nil
		}

		// Snapshot current digest table BEFORE truncating so we capture the full period.
		server.FlushPFSSnapshotToLog()

		// Enrich the in-memory table graph with join weights derived from the
		// explain plans collected during the period that just ended.
		// We reload the tablemap here (no columns/indexes needed — just the graph
		// skeleton) so the workload links are written into a fresh structure that
		// the cluster can attach to its next schema refresh.
		if cluster.Conf.MonitorPFSQueriesExplain {
			tablemap, _, _, tmErr := dbhelper.GetTables(server.Conn, server.DBVersion, false, false, 10)
			if tmErr == nil {
				server.ApplyPFSJoinLinksToSchema(tablemap)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"PFS join links: could not load tablemap for %s: %v", server.URL, tmErr)
			}
		}

		// Build a fresh context for this period's explain goroutine.
		// Stored on the server so the next snapshot (or a server shutdown) can cancel it.
		ctx, cancel := context.WithCancel(context.Background())
		server.pfsExplainCancel = cancel

		// Launch explain in a goroutine — inter-explain delay × N digests can take
		// minutes; we must never block the TRUNCATE or the monitoring cycle.
		// The goroutine receives a *snapshot* of the current PFSQueries map so it
		// works on the period that just ended, not on whatever the map contains
		// when each individual EXPLAIN fires.
		// The wrapper nils pfsExplainCancel on natural completion (Bug 6 fix):
		// pfsExplainCancel is only ever written from this monitoring-loop goroutine,
		// so the nil is safe — no other writer can race with it.
		snapshot := server.PFSQueries.ToNewMap()
		go func() {
			server.RunPFSExplainCapture(ctx, snapshot)
			server.pfsExplainCancel = nil
		}()

		// Reset counters so the next window starts clean.
		logs, err := dbhelper.TruncatePFSStatements(server.Conn)
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg,
			"Could not truncate PFS statements summary on %s: %s", server.URL, err)
		server.PFSLastSnapshot = time.Now()
	}

	// Purge stale explain cache entries once per day, independently of the snapshot
	// period. Running daily is frequent enough to honour the configured retention
	// while avoiding unnecessary disk rewrites on every 2-second monitoring tick.
	if cluster.Conf.MonitorPFSQueriesExplain &&
		cluster.Conf.MonitorPFSQueriesExplainPurgePeriod > 0 &&
		time.Since(server.PFSLastExplainPurge) >= 24*time.Hour {
		server.PurgePFSExplainCache()
		server.PFSLastExplainPurge = time.Now()
	}

	// Always refresh the in-memory map for the live GUI view.
	pfsq, logs, err := dbhelper.GetQueries(server.Conn, server.DBVersion)
	server.PFSQueries = dbhelper.FromNormalPFSMap(server.PFSQueries, pfsq)
	cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg,
		"Could not get queries %s %s", server.URL, err)
}

// PFSSnapshotEntry is the JSON structure written to the periodic snapshot log.
// One line per digest — every field needed to replay EXPLAIN later without
// having to hit the live Performance Schema again.
type PFSSnapshotEntry struct {
	Timestamp    string  `json:"timestamp"`
	Digest       string  `json:"digest"`
	DigestText   string  `json:"digestText"`
	SampleQuery  string  `json:"sampleQuery"`
	SchemaName   string  `json:"schemaName"`
	ExecCount    int64   `json:"execCount"`
	ExecTimeAvg  float64 `json:"execTimeAvgMs"`
	ExecTimeMax  float64 `json:"execTimeMaxMs"`
	RowsScanned  int64   `json:"rowsScanned"`
	PlanFullScan string  `json:"planFullScan"`
	PlanTmpDisk  int64   `json:"planTmpDisk"`
	ErrCount     int64   `json:"errCount"`
}

// FlushPFSSnapshotToLog writes one JSON-lines file per hour containing every
// digest collected during the period, including a concrete sample_query so that
// EXPLAIN can be replayed offline.  The file is named:
//
//	<Datadir>/log/log_pfs_queries_<YYYYMMDD_HH>.jsonl
//
// Rotation: if the file grows beyond 100 MB it is truncated before writing
// (same policy as the slow-query log file in GetSlowLogTable).
func (server *ServerMonitor) FlushPFSSnapshotToLog() {
	cluster := server.ClusterGroup

	// We need an up-to-date fetch to get sample queries before the TRUNCATE.
	pfsq, _, err := dbhelper.GetQueries(server.Conn, server.DBVersion)
	if err != nil || len(pfsq) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg,
			"PFS snapshot: nothing to flush on %s", server.URL)
		return
	}

	os.MkdirAll(server.Datadir+"/log", 0755)

	now := time.Now()
	filename := server.Datadir + "/log/log_pfs_queries_" + now.Format("20060102_15") + ".jsonl"

	// Open flags depend on whether rotation is needed.
	// O_APPEND cannot be combined with truncation on Linux — the kernel ignores
	// any Seek after Truncate when O_APPEND is set.  If the file exceeds the
	// rotation threshold we reopen it with O_TRUNC instead.
	flags := os.O_CREATE | os.O_APPEND | os.O_WRONLY
	if fi, statErr := os.Stat(filename); statErr == nil && fi.Size() > 100*1024*1024 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}

	f, err := os.OpenFile(filename, flags, 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"PFS snapshot: could not open log file %s: %v", filename, err)
		return
	}
	defer f.Close()

	ts := now.Format(time.RFC3339)
	written := 0
	writeErrorLogged := false
	for _, q := range pfsq {
		entry := PFSSnapshotEntry{
			Timestamp:    ts,
			Digest:       q.Digest,
			DigestText:   q.Digest_text,
			SampleQuery:  q.Sample_query,
			SchemaName:   q.Schema_name,
			ExecCount:    q.Exec_count,
			ExecTimeAvg:  q.Exec_time_avg_ms.Float64,
			ExecTimeMax:  q.Exec_time_max.Float64,
			RowsScanned:  q.Rows_scanned,
			PlanFullScan: q.Plan_full_scan,
			PlanTmpDisk:  q.Plan_tmp_disk,
			ErrCount:     q.Err_count,
		}
		line, jsonErr := json.Marshal(entry)
		if jsonErr != nil {
			continue
		}
		if _, werr := f.Write(append(line, '\n')); werr != nil {
			if !writeErrorLogged {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
					"PFS snapshot: write error on %s: %v — remaining records dropped", filename, werr)
				writeErrorLogged = true
			}
			break
		}
		written++
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"PFS snapshot: wrote %d digest entries to %s", written, filename)
}

// pfsExplainCachePath returns the canonical path for this server's explain plan cache file.
// The file is stored under the server Datadir so it survives restarts and is
// co-located with the other per-server state files (serverstate.json, slow-query log, …).
//
//	<Datadir>/log/pfs_explain_cache.jsonl
//
// The format is JSON-lines: one PFSExplainRecord per line, one line per unique digest.
// New digests are appended; existing ones are never rewritten (write-once semantics).
func (server *ServerMonitor) pfsExplainCachePath() string {
	return server.Datadir + "/log/pfs_explain_cache.jsonl"
}

// LoadPFSExplainCache reads the on-disk explain cache into server.PFSExplainCache.
// Called once during server initialisation so cached plans survive a process restart
// and are immediately available to the API without waiting for the next snapshot cycle.
func (server *ServerMonitor) LoadPFSExplainCache() {
	cluster := server.ClusterGroup
	path := server.pfsExplainCachePath()

	data, err := os.ReadFile(path)
	if err != nil {
		// File simply does not exist yet — not an error.
		return
	}

	// Parse all lines first (no lock needed — called single-threaded at startup).
	// Last-wins per digest: refreshed records are appended after their predecessors,
	// so iterating the full file and overwriting naturally deduplicates.
	tmp := make(map[string]PFSExplainRecord)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec PFSExplainRecord
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"LoadPFSExplainCache: skipping malformed line in %s: %v", path, jsonErr)
			continue
		}
		if rec.Digest != "" {
			tmp[rec.Digest] = rec
		}
	}

	// Single lock acquisition to copy the parsed map into the live cache.
	server.PFSExplainCacheMu.Lock()
	for digest, rec := range tmp {
		server.PFSExplainCache[digest] = rec
	}
	server.PFSExplainCacheMu.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"LoadPFSExplainCache: loaded %d cached explain plans from %s", len(tmp), path)
}

// PurgePFSExplainCache removes explain records older than MonitorPFSQueriesExplainPurgePeriod
// days from both the in-memory map and the on-disk cache file.
//
// Disk rewrite strategy: because the cache file is append-only, purging requires a
// full rewrite. We write surviving records to a temp file in the same directory,
// then atomically replace the original with os.Rename. This guarantees the file is
// never left in a partial state even if the process is killed mid-purge.
//
// The function is a no-op when:
//   - MonitorPFSQueriesExplainPurgePeriod is 0 (purge disabled)
//   - The cache is empty
//   - No entry is old enough to evict
func (server *ServerMonitor) PurgePFSExplainCache() {
	cluster := server.ClusterGroup

	purgeDays := cluster.Conf.MonitorPFSQueriesExplainPurgePeriod
	if purgeDays <= 0 {
		return // purge disabled
	}

	cutoff := time.Now().AddDate(0, 0, -purgeDays)

	// --- Phase 1: classify entries under the lock (fast, no I/O) ---
	server.PFSExplainCacheMu.Lock()

	if len(server.PFSExplainCache) == 0 {
		server.PFSExplainCacheMu.Unlock()
		return
	}

	survivors := make([]PFSExplainRecord, 0, len(server.PFSExplainCache))
	evictedKeys := make([]string, 0)
	noDate := 0

	for digest, rec := range server.PFSExplainCache {
		capturedAt, parseErr := time.Parse(time.RFC3339, rec.CapturedAt)
		if parseErr != nil {
			noDate++
			survivors = append(survivors, rec)
			continue
		}
		if capturedAt.Before(cutoff) {
			evictedKeys = append(evictedKeys, digest)
		} else {
			survivors = append(survivors, rec)
		}
	}

	server.PFSExplainCacheMu.Unlock()

	if len(evictedKeys) == 0 {
		return // nothing old enough — skip disk rewrite
	}

	// --- Phase 2: rewrite disk file (slow I/O, lock NOT held) ---
	path := server.pfsExplainCachePath()
	os.MkdirAll(server.Datadir+"/log", 0755)

	tmpPath := path + ".purge.tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"PurgePFSExplainCache: cannot create temp file %s: %v", tmpPath, err)
		return
	}

	writeErr := false
	for _, rec := range survivors {
		line, jsonErr := json.Marshal(rec)
		if jsonErr != nil {
			continue
		}
		if _, werr := f.Write(append(line, '\n')); werr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"PurgePFSExplainCache: write error on temp file %s: %v", tmpPath, werr)
			writeErr = true
			break
		}
	}
	f.Close()

	if writeErr {
		os.Remove(tmpPath)
		return
	}

	if err := os.Rename(tmpPath, path); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"PurgePFSExplainCache: cannot rename %s \u2192 %s: %v", tmpPath, path, err)
		os.Remove(tmpPath)
		return
	}

	// --- Phase 3: evict from in-memory map under lock (fast) ---
	server.PFSExplainCacheMu.Lock()
	for _, digest := range evictedKeys {
		delete(server.PFSExplainCache, digest)
	}
	server.PFSExplainCacheMu.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"PurgePFSExplainCache: evicted %d records older than %d days (%d survivors, %d unparseable) from %s",
		len(evictedKeys), purgeDays, len(survivors)-noDate, noDate, path)
}

// explainWorkItem is one unit of work for RunPFSExplainCapture, pre-sorted by priority.
type explainWorkItem struct {
	digest     string
	query      *dbhelper.PFSQuery
	capturedAt time.Time // zero if never explained — sorts first (highest priority)
}

// RunPFSExplainCapture iterates over the digests captured during the just-completed
// snapshot period, running EXPLAIN once per template and persisting the plan to the
// on-disk cache file.
//
// Cancellation and ordering:
//   - ctx is cancelled by the next snapshot trigger (or server shutdown), so a
//     slow in-flight run is cleanly interrupted rather than left as an orphan.
//   - snapshot is a frozen copy of PFSQueries taken just before the TRUNCATE, so
//     the goroutine always works on the period that just ended regardless of how
//     long it takes.
//   - Work items are sorted so digests with NO cached plan come first, then digests
//     whose plan is oldest (most likely to be stale after schema/stats changes).
//     This means a partial run (interrupted by the next snapshot) will always
//     have covered the highest-value digests first.
//   - The in-memory cache and the disk file are updated under PFSExplainCacheMu so
//     concurrent reads from the API handlers never observe torn state.
func (server *ServerMonitor) RunPFSExplainCapture(ctx context.Context, snapshot map[string]*dbhelper.PFSQuery) {
	cluster := server.ClusterGroup

	if !cluster.Conf.MonitorPFSQueriesExplain {
		return
	}

	delayMs := cluster.Conf.MonitorPFSQueriesExplainDelay
	if delayMs < 0 {
		delayMs = 0
	}

	// --- Build priority-sorted work list ---
	work := make([]explainWorkItem, 0, len(snapshot))
	for digest, q := range snapshot {
		if q.Sample_query == "" {
			continue
		}
		// Skip statements that can't be EXPLAINed and repman's own queries
		upper := strings.ToUpper(strings.TrimLeft(q.Sample_query, " \t\n/*"))
		if strings.HasPrefix(upper, "EXPLAIN") {
			continue
		}
		canExplain := strings.HasPrefix(upper, "SELECT") ||
			strings.HasPrefix(upper, "DELETE") ||
			strings.HasPrefix(upper, "INSERT") ||
			strings.HasPrefix(upper, "UPDATE") ||
			strings.HasPrefix(upper, "REPLACE")
		if !canExplain {
			continue
		}
		if strings.Contains(q.Sample_query, "replication-manager") {
			continue
		}
		// Skip queries that could trigger side effects via EXPLAIN
		// (stored procedures/functions can execute with elevated privileges)
		upperFull := strings.ToUpper(q.Sample_query)
		if strings.Contains(upperFull, "CALL ") ||
			strings.Contains(upperFull, "EXECUTE ") ||
			strings.Contains(upperFull, "PREPARE ") ||
			strings.Contains(upperFull, "INFORMATION_SCHEMA") ||
			strings.Contains(upperFull, "PERFORMANCE_SCHEMA") ||
			strings.Contains(upperFull, "MYSQL.") {
			continue
		}
		var capturedAt time.Time
		server.PFSExplainCacheMu.Lock()
		if rec, exists := server.PFSExplainCache[digest]; exists {
			capturedAt, _ = time.Parse(time.RFC3339, rec.CapturedAt)
		}
		server.PFSExplainCacheMu.Unlock()
		work = append(work, explainWorkItem{
			digest:     digest,
			query:      q,
			capturedAt: capturedAt,
		})
	}

	sort.Slice(work, func(i, j int) bool {
		zi := work[i].capturedAt.IsZero()
		zj := work[j].capturedAt.IsZero()
		if zi != zj {
			return zi
		}
		return work[i].capturedAt.Before(work[j].capturedAt)
	})

	if len(work) == 0 {
		return
	}

	os.MkdirAll(server.Datadir+"/log", 0755)
	path := server.pfsExplainCachePath()

	// Bug 2: only NEW plans are appended to the cache file; refreshed (already-cached)
	// plans update the in-memory map but do NOT append a duplicate line to disk.
	// The on-disk file therefore grows only when genuinely new templates appear.
	// Duplicate entries introduced before this fix are deduplicated on the next
	// LoadPFSExplainCache (last-wins iteration) or purge rewrite.
	appendFile, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"RunPFSExplainCapture: cannot open cache file %s: %v", path, err)
		return
	}
	defer appendFile.Close()

	now := time.Now().Format(time.RFC3339)
	newPlans := 0
	refreshed := 0
	interrupted := 0
	first := true

	for _, item := range work {
		select {
		case <-ctx.Done():
			interrupted = len(work) - newPlans - refreshed
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"RunPFSExplainCapture: cancelled by new snapshot on %s — %d/%d explains done, %d interrupted",
				server.URL, newPlans+refreshed, len(work), interrupted)
			return
		default:
		}

		// Bug 5: use time.NewTimer + Stop so the timer goroutine is always cleaned up,
		// even when the context fires first.
		if !first && delayMs > 0 {
			t := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
			select {
			case <-ctx.Done():
				t.Stop()
				interrupted = len(work) - newPlans - refreshed
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
					"RunPFSExplainCapture: cancelled during delay on %s — %d/%d explains done, %d interrupted",
					server.URL, newPlans+refreshed, len(work), interrupted)
				return
			case <-t.C:
			}
		}
		first = false

		server.PFSExplainCacheMu.Lock()
		_, alreadyCached := server.PFSExplainCache[item.digest]
		server.PFSExplainCacheMu.Unlock()

		plan, explainErr := server.GetQueryExplain(item.query.Schema_name, item.query.Sample_query)
		if explainErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"RunPFSExplainCapture: EXPLAIN failed for digest %s on %s: %v",
				item.digest, server.URL, explainErr)
			continue
		}

		rec := PFSExplainRecord{
			CapturedAt:  now,
			Digest:      item.digest,
			DigestText:  item.query.Digest_text,
			SampleQuery: item.query.Sample_query,
			SchemaName:  item.query.Schema_name,
			Plan:        plan,
		}

		// Only write NEW plans to disk; refreshes update memory only (Bug 2).
		if !alreadyCached {
			line, jsonErr := json.Marshal(rec)
			if jsonErr == nil {
				if _, werr := appendFile.Write(append(line, '\n')); werr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
						"RunPFSExplainCapture: write error on %s: %v — stopping disk writes", path, werr)
					// Update memory but stop trying to write to disk for this run.
					server.PFSExplainCacheMu.Lock()
					server.PFSExplainCache[item.digest] = rec
					server.PFSExplainCacheMu.Unlock()
					newPlans++
					break
				}
			}
			newPlans++
		} else {
			refreshed++
		}

		server.PFSExplainCacheMu.Lock()
		server.PFSExplainCache[item.digest] = rec
		server.PFSExplainCacheMu.Unlock()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"RunPFSExplainCapture: %d new plans written to disk, %d refreshed in memory, %d skipped (no sample), delay %dms on %s",
		newPlans, refreshed, len(snapshot)-len(work), delayMs, server.URL)
}

func (server *ServerMonitor) GetPFSStatements() []dbhelper.PFSQuery {
	rows := make([]dbhelper.PFSQuery, 0)
	for _, v := range server.PFSQueries.ToNewMap() {
		rows = append(rows, *v)
	}
	misc.SortByKeyDesc(rows, func(q dbhelper.PFSQuery) float64 { v, _ := strconv.ParseFloat(q.Value, 64); return v })
	return rows
}

func (server *ServerMonitor) GetPFSStatementsSlowLog() []dbhelper.PFSQuery {
	SlowPFSQueries := make(map[string]dbhelper.PFSQuery)
	for _, s := range server.SlowLog.Buffer {
		if s.Query != "" {
			if val, ok := SlowPFSQueries[s.Digest]; ok {
				val.Exec_count = val.Exec_count + 1
				sum, _ := strconv.ParseFloat(val.Exec_time_total, 64)
				val.Exec_time_total = strconv.FormatFloat(s.TimeMetrics["queryTime"]/1000+sum, 'g', 1, 64)
				avg, _ := strconv.ParseFloat(val.Exec_time_total, 64)
				avg = avg / float64(val.Exec_count)
				val.Exec_time_avg_ms.Float64 = avg
				if s.TimeMetrics["queryTime"] > val.Exec_time_max.Float64 {
					val.Exec_time_max.Float64 = s.TimeMetrics["queryTime"]
				}
				val.Value = val.Exec_time_total
				SlowPFSQueries[s.Digest] = val
			} else {
				var nval dbhelper.PFSQuery
				nval.Digest_text = dbhelper.GetQueryDigest(s.Query)
				nval.Digest = s.Digest
				nval.Query = s.Query
				nval.Last_seen = s.Timestamp
				nval.Exec_count = 1
				nval.Exec_time_total = strconv.FormatFloat(s.TimeMetrics["queryTime"]/1000, 'g', 1, 64)
				nval.Exec_time_max.Float64 = s.TimeMetrics["queryTime"]
				nval.Value = nval.Exec_time_total
				avg, _ := strconv.ParseFloat(nval.Exec_time_total, 64)
				avg = avg / float64(nval.Exec_count)
				nval.Exec_time_avg_ms.Float64 = avg
				nval.Rows_scanned = int64(s.NumberMetrics["rowsExamined"])
				nval.Rows_sent = int64(s.NumberMetrics["rowsSent"])
				SlowPFSQueries[s.Digest] = nval
				//	val.Plan_tmp_disk = s.BoolMetrics[""]
			}
		}
	}
	var rows []dbhelper.PFSQuery
	for _, v := range SlowPFSQueries {
		rows = append(rows, v)
	}
	misc.SortByKeyDesc(rows, func(q dbhelper.PFSQuery) float64 { v, _ := strconv.ParseFloat(q.Value, 64); return v })
	var limits []dbhelper.PFSQuery
	i := 0
	for _, v := range rows {
		if i < 50 {
			limits = append(limits, v)

		}
		i = i + 1
	}
	return limits
}

func (server *ServerMonitor) GetSlowLog() []dbhelper.PFSQuery {
	rows := make([]dbhelper.PFSQuery, 0)

	for _, s := range server.SlowLog.Buffer {
		if s.Query != "" {
			var nval dbhelper.PFSQuery
			nval.Digest_text = dbhelper.GetQueryDigest(s.Query)
			nval.Digest = s.Digest
			nval.Query = s.Query
			nval.Last_seen = s.Timestamp
			nval.Exec_count = 1
			nval.Exec_time_total = strconv.FormatFloat(s.TimeMetrics["queryTime"]/1000, 'g', 1, 64)
			nval.Exec_time_max.Float64 = s.TimeMetrics["queryTime"]
			nval.Value = nval.Exec_time_total
			avg, _ := strconv.ParseFloat(nval.Exec_time_total, 64)
			avg = avg / float64(nval.Exec_count)
			nval.Exec_time_avg_ms.Float64 = avg
			nval.Rows_scanned = int64(s.NumberMetrics["rowsExamined"])
			nval.Rows_sent = int64(s.NumberMetrics["rowsSent"])
			nval.Schema_name = s.Db

			//	val.Plan_tmp_disk = s.BoolMetrics[""]

			rows = append(rows, nval)
		}
	}
	misc.SortByKeyDesc(rows, func(q dbhelper.PFSQuery) float64 { v, _ := strconv.ParseFloat(q.Value, 64); return v })
	return rows
}

func (server *ServerMonitor) GetNewDBConn() (*sqlx.DB, error) {
	// get topology is call to late
	if server.ClusterGroup.Conf.MasterSlavePgStream || server.ClusterGroup.Conf.MasterSlavePgLogical {
		return sqlx.Connect("postgres", server.DSN)

	}
	conn, err := sqlx.Connect("mysql", server.DSN)
	if err != nil && !server.ClusterGroup.HaveDBTLSCert && !server.ForceTLSSkipVerify {
		if driverErr, ok := err.(*mysql.MySQLError); ok && driverErr.Number == 3159 {
			// Server has require_secure_transport=ON but we connected without SSL.
			// Auto-upgrade to skip-verify TLS and persist so future connections skip this retry.
			server.ForceTLSSkipVerify = true
			server.SetDSN()
			conn, err = sqlx.Connect("mysql", server.DSN)
			if err != nil {
				server.ForceTLSSkipVerify = false
				server.SetDSN()
			} else {
				// Close the persistent pool (server.Conn) so Ping() replaces it with
				// this TLS-enabled conn immediately, avoiding further plaintext attempts.
				if server.Conn != nil {
					server.Conn.Close()
					server.Conn = nil
				}
				// Propagate to cluster so proxies also enable SSL on their backends.
				// Use the cluster mutex: multiple server goroutines may set this concurrently.
				server.ClusterGroup.Lock()
				server.ClusterGroup.HaveAutoTLS = true
				server.ClusterGroup.Unlock()
				server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
					"Auto-enabled TLS with InsecureSkipVerify=true for %s (error 3159: require_secure_transport=ON)."+
						" Certificate authenticity is NOT verified — configure monitoring-ssl-ca/cert/key for full TLS validation.", server.URL)
			}
			return conn, err
		}
	}
	if err != nil && server.ClusterGroup.HaveDBTLSCert {
		// Possible can't connect because of SSL key rotation try old key until server rebooted or key reloaded
		server.TLSConfigUsed = ConstTLSOldConfig
		server.SetDSN()
		conn, err := sqlx.Connect("mysql", server.DSN)
		if err == nil {
			server.LastTLSConfig = server.TLSConfigUsed // Track last working TLS config
			server.ClusterGroup.SetState("ERR00080", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00080"], server.URL), ServerUrl: server.URL, ErrFrom: "MON"})
		} else {
			server.TLSConfigUsed = ConstTLSNoConfig
			server.SetDSN()
			conn, err := sqlx.Connect("mysql", server.DSN)
			if err == nil {
				server.LastTLSConfig = server.TLSConfigUsed // Track last working TLS config

				// if not –require_secure_transport can still connect with no certificate MDEV-13362
				//server.ClusterGroup.SetState("ERR00081", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00081"], server.URL), ServerUrl: server.URL, ErrFrom: "MON"})
			}
			server.TLSConfigUsed = ConstTLSCurrentConfig
			server.SetDSN()
			return conn, err
		}
		//reset DNS in case the server is restarted
		server.TLSConfigUsed = ConstTLSCurrentConfig
		conn.SetConnMaxLifetime(3595 * time.Second)
		server.SetDSN()
		return conn, err
	}

	if err == nil {
		server.LastTLSConfig = server.TLSConfigUsed // Track last working TLS config
	}

	return conn, err

}

func (server *ServerMonitor) GetSlowLogTable(wg *sync.WaitGroup) error {
	cluster := server.ClusterGroup
	defer wg.Done()

	if cluster.IsInFailover() {
		return nil
	}

	// Skip if server is in backup state and is backup server
	if cluster.IsInBackup() {
		bcksrv := cluster.GetBackupServer()
		if bcksrv != nil && bcksrv.URL == server.URL {
			return nil
		}
	}

	if !server.HasLogsInSystemTables() {
		return nil
	}
	if server.IsDown() {
		return nil
	}

	// Skip if server is in reseeding (restore from backup) state
	if server.HasAnyReseedingState() {
		return nil
	}

	if !cluster.GetConf().MonitorQueries {
		return nil
	}
	if server.IsInSlowQueryCapture {
		return nil
	}
	server.IsInSlowQueryCapture = true
	defer func() { server.IsInSlowQueryCapture = false }()

	currentTime := time.Now()
	timeStampString := currentTime.Format("20060102")
	timeout := time.Duration(cluster.Conf.ReadTimeout) * time.Second

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	Conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Failed to connect on %s: %v", server.URL, err)
	}
	defer Conn.Close()

	// Move previous batch and truncate if still exists
	dbhelper.MoveLogsToDailyTable(Conn, server.DBVersion, "slow_log", timeStampString, timeout)

	if err := dbhelper.FetchLogsToBufferTable(Conn, server.DBVersion, "slow_log", timeStampString, timeout); err != nil {
		return fmt.Errorf("Error fetch slow logs to buffer table on %s. Err : %v", server.URL, err)
	}

	if err := dbhelper.TruncateLogsTable(Conn, server.DBVersion, "slow_log", timeout); err != nil {
		return fmt.Errorf("Error truncate slow logs buffer table on %s. Err : %v", server.URL, err)
	}

	os.MkdirAll(server.Datadir+"/log", 0755)

	f, err := os.OpenFile(server.Datadir+"/log/log_slow_query.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("Error writing slow queries logs on %s. Err : %v", server.URL, err)
	}
	fi, _ := f.Stat()
	if fi.Size() > 100000000 {
		f.Truncate(0)
		f.Seek(0, 0)
	}
	defer f.Close()

	slowqueries := []dbhelper.LogSlow{}
	query := "SELECT FLOOR(UNIX_TIMESTAMP(start_time)) as start_time, user_host,TIME_TO_SEC(query_time) AS query_time,TIME_TO_SEC(lock_time) AS lock_time,rows_sent,rows_examined,db,last_insert_id,insert_id,server_id,sql_text,thread_id, 0 as rows_affected FROM  replication_manager_schema.slow_log_buffer WHERE start_time > FROM_UNIXTIME(" + strconv.FormatInt(server.MaxSlowQueryTimestamp+1, 10) + ")"
	err = server.ConnSelectQueryWithTimeout(Conn, time.Duration(cluster.Conf.ReadTimeout)*time.Second, &slowqueries, query)
	if err != nil {
		return fmt.Errorf("Error selecting slow queries logs in buffer table on %s: %v", server.URL, err)
	}
	for _, s := range slowqueries {
		fmt.Fprintf(f, "# User@Host: %s\n# Thread_id: %d  Schema: %s  QC_hit: No\n# Query_time: %s  Lock_time: %s  Rows_sent: %d  Rows_examined: %d\n# Rows_affected: %d\nSET timestamp=%d;\n%s;\n",
			s.User_host.String,
			s.Thread_id,
			s.Db.String,
			s.Query_time,
			s.Lock_time,
			s.Rows_sent,
			s.Rows_examined,
			s.Rows_affected,
			s.Start_time,
			strings.ReplaceAll(strings.ReplaceAll(s.Sql_text.String, "\r\n", " "), "\n", " "),
		)
		server.MaxSlowQueryTimestamp = s.Start_time
	}

	if err := dbhelper.MoveLogsToDailyTable(Conn, server.DBVersion, "slow_log", timeStampString, timeout); err != nil {
		return fmt.Errorf("Error moving logs from buffer to daily table on %s: %v", server.URL, err)
	}

	server.RotateSystemLogs()
	time.Sleep(60 * time.Second)
	//	server.ExecQueryNoBinLog("TRUNCATE mysql.slow_log")
	return nil
}

func (server *ServerMonitor) GetTables() []dbhelper.Table {
	if server.Tables == nil {
		server.Tables = make([]dbhelper.Table, 0)
	}
	return server.Tables
}

func (server *ServerMonitor) GetVTables() map[string]*dbhelper.Table {
	return server.DictTables.ToNewMap()
}

func (server *ServerMonitor) GetDictTables() []*dbhelper.Table {
	cluster := server.ClusterGroup
	var tables []*dbhelper.Table
	if server.IsFailed() {
		return tables
	}

	for _, t := range server.DictTables.ToNewMap() {
		tables = append(tables, t)
	}

	master := server.ClusterGroup.GetMaster()
	if len(tables) == 0 && master != nil && master.URL == server.URL {
		cluster.SetWaitMonitorSchema()
	}

	misc.SortByKeyDesc(tables, func(t *dbhelper.Table) int64 { return t.DataLength + t.IndexLength })
	return tables
}

func (server *ServerMonitor) GetInnoDBStatus() []dbhelper.Variable {
	var status []dbhelper.Variable
	getf := func(key any, value any) bool {
		k := key.(string)
		v := value.(string)

		var r dbhelper.Variable
		r.Variable_name = k
		r.Value = v
		status = append(status, r)
		return true
	}
	server.EngineInnoDB.Callback(getf)
	misc.SortByKey(status, func(v dbhelper.Variable) string { return v.Variable_name })
	return status

}

func (server *ServerMonitor) GetTableDefinition(schema string, table string) (string, error) {
	cluster := server.ClusterGroup
	query := "SHOW CREATE TABLE `" + schema + "`.`" + table + "`"
	var tbl, ddl string

	err := server.Conn.QueryRowx(query).Scan(&tbl, &ddl)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed query %s %s", query, err)
		return "", err
	}
	return ddl, nil
}

func (server *ServerMonitor) GetDatabaseBasedir() string {

	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.Datadir

	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir
	}
	return server.Datadir
}

func (server *ServerMonitor) GetTablePK(schema string, table string) (string, error) {
	query := "SELECT group_concat(distinct column_name order by ORDINAL_POSITION) from information_schema.KEY_COLUMN_USAGE WHERE CONSTRAINT_NAME='PRIMARY' AND CONSTRAINT_SCHEMA='" + schema + "' AND TABLE_NAME='" + table + "'"
	var pk string
	err := server.Conn.QueryRowx(query).Scan(&pk)
	// No need to log error as it will be logged in the caller function and it is not a critical error if we can't get PK for a table
	if err != nil {
		return "", err
	}
	return pk, nil
}

func (server *ServerMonitor) GetTableColumDef(schema string, table string, column string) string {
	t := server.DictTables.Get(schema + "." + table)
	for _, c := range t.TableColumns {
		if c.Name == column {
			return c.Type
		}
	}
	return ""
}

func (server *ServerMonitor) GetVersion() *version.Version {
	return server.DBVersion
}

func (server *ServerMonitor) GetCluster() *Cluster {
	return server.ClusterGroup
}

func (server *ServerMonitor) GetClusterConfig() *config.Config {
	return server.GetCluster().Conf
}

func (server *ServerMonitor) GetGroupReplicationLocalAddress() string {
	strPort := "33061"
	return server.Host + ":" + strPort
}

func (server *ServerMonitor) GetWsrepNodeAddress() string {
	/*	strPort := "4567"*/
	return server.Host /*+ ":" + strPort*/
}

func (server *ServerMonitor) GetCPUUsageFromStats(t time.Time) (float64, error) {
	last_busy_time, _ := strconv.ParseFloat(server.WorkLoad.Get("current").BusyTime, 64)
	t_now := time.Now()
	elapsed := t_now.Sub(t).Seconds()
	if server.DBVersion.IsMariaDB() && last_busy_time != 0 {

		//if db using user_statistics, then we get cpu_usage from the user_statistics
		if server.HasUserStats() {
			res, _, err := dbhelper.GetCPUUsageFromUserStats(server.Conn)
			if err == nil {
				busy_time, _ := strconv.ParseFloat(res, 64)
				core, _ := strconv.ParseFloat(server.GetCluster().Conf.ProvCores, 64)
				return ((busy_time - last_busy_time) / (core * elapsed)) * 100, nil
			}
		}
		return 0, nil
	}
	return 0, errors.New("Not mariaDB version, cannot compute cpu usage")
}

func (server *ServerMonitor) GetBusyTimeFromStats() (string, error) {
	if server.DBVersion.IsMariaDB() {

		//if db using user_statistics, then we get cpu_usage from the user_statistics
		if server.HasUserStats() {
			res, _, err := dbhelper.GetCPUUsageFromUserStats(server.Conn)
			if err == nil {

				return res, nil
			}
		}
		return "", nil
	}
	return "", errors.New("Not mariaDB version, cannot compute cpu usage")
}

func (server *ServerMonitor) GetCPUUsageFromThreadsPool() float64 {
	if server.DBVersion.IsMariaDB() {
		//we compute it from status
		thread_idle, _ := strconv.ParseFloat(server.Status.Get("THREADPOOL_IDLE_THREADS"), 64)
		thread, _ := strconv.ParseFloat(server.Status.Get("THREADPOOL_THREADS"), 64)
		core, _ := strconv.ParseFloat(server.GetCluster().Conf.ProvCores, 64)
		return ((thread - thread_idle) / core) * 100
	}
	return -1
}

func (server *ServerMonitor) GetSSLClientParam(tool string) []string {
	var noSSLParams, skipVerify bool
	var cacertfile, clicertfile, clikeyfile, path, sslMode string
	cluster := server.ClusterGroup
	ver := cluster.VersionsMap.Get(tool)
	path = cluster.WorkingDir

	// If we have working SSL in Go-MySQL
	if server.LastTLSConfig != ConstTLSNoConfig {
		if server.LastTLSConfig == ConstTLSOldConfig {
			path = path + "/old_certs"
		}

		// If any of the SSL files are empty, we need to use the generated certs (or use Zero Config SSL MariaDB 11.3+)
		if cluster.Conf.HostsTLSCA == "" || cluster.Conf.HostsTlsCliCert == "" || cluster.Conf.HostsTlsCliKey == "" {
			if cluster.Conf.DBServersTLSUseGeneratedCertificate || cluster.Configurator.HaveDBTag("ssl") || server.HasSSL() {
				// Use generated certificate, add skipVerify
				skipVerify = true

				// Use Zero Config SSL certificate
				if server.DBVersion.IsMariaDBGreater113() && !cluster.Configurator.HaveDBTag("ssl") {
					noSSLParams = true // Auto SSL Zero Config SSL MariaDB 11.3+
				}

				if cluster.Configurator.HaveDBTag("ssl") {
					cacertfile = path + "/ca-cert.pem"
					clicertfile = path + "/client-cert.pem"
					clikeyfile = path + "/client-key.pem"
				}

			} else {
				noSSLParams = true
			}
		} else {
			cacertfile = cluster.Conf.HostsTLSCA
			clicertfile = cluster.Conf.HostsTlsCliCert
			clikeyfile = cluster.Conf.HostsTlsCliKey
		}

		// Add SSL params
		if !noSSLParams {
			sslMode = cluster.Conf.HostsTlsSslMode

			if cluster.Conf.HostsTlsSslMode == "" && skipVerify {
				sslMode = "REQUIRED" // No verify server cert
			}

			if cacertfile != "" && clicertfile != "" && clikeyfile != "" {
				if cluster.Conf.HostsTlsSslMode == "" && !skipVerify {
					sslMode = "VERIFY_CA" // Only verify CA if no SSL mode is set
				}

				if ver.IsMySQLOrPerconaGreater8() { // client removed --ssl support in 8.0, use --ssl-mode
					switch sslMode {
					case "DISABLED":
						return []string{"--ssl-mode=DISABLED"}
					case "PREFERRED", "REQUIRED":
						return []string{"--ssl-mode=" + sslMode} // No verify server cert
					case "VERIFY_CA":
						return []string{"--ssl-mode=" + sslMode, "--ssl-ca=" + cacertfile}
					case "VERIFY_IDENTITY":
						return []string{"--ssl-mode=" + sslMode, "--ssl-ca=" + cacertfile, "--ssl-cert=" + clicertfile, "--ssl-key=" + clikeyfile}
					}
				} else { // Use old","--ssl equivalent
					switch sslMode {
					case "DISABLED":
						if tool == "client-dump" || tool == "client-binlog" {
							return []string{"--ssl=FALSE"}
						}
						return []string{"--skip-ssl"}
					case "PREFERRED", "REQUIRED":
						return []string{"--ssl", "--ssl-verify-server-cert=false"}
					case "VERIFY_CA":
						return []string{"--ssl", "--ssl-verify-server-cert", "--ssl-ca=" + cacertfile}
					case "VERIFY_IDENTITY":
						return []string{"--ssl", "--ssl-verify-server-cert", "--ssl-ca=" + cacertfile, "--ssl-cert=" + clicertfile, "--ssl-key=" + clikeyfile}
					}
				}
			} else {
				if ver.IsMySQLOrPerconaGreater8() { // client removed --ssl support in 8.0, use --ssl-mode
					if sslMode == "" {
						return []string{} // Use default SSL mode of client
					} else {
						return []string{"--ssl-mode=" + sslMode} // No verify server cert
					}
				} else { // Use old","--ssl equivalent
					if sslMode == "DISABLED" {
						if tool == "client-dump" || tool == "client-binlog" {
							return []string{"--ssl=FALSE"}
						}
						return []string{"--skip-ssl"}
					}

					// No verify server cert
					return []string{"--ssl", "--ssl-verify-server-cert=false"}
				}
			}
		}
	}

	// Only add for client dist 11.3 onwards, and DB pre 11.3
	if !server.DBVersion.IsMariaDBGreater113() && ver.IsMariaDBGreater113() {
		if server.HasSSL() {
			return []string{"--ssl", "--ssl-verify-server-cert=false"}
		} else {
			switch tool {
			case "client":
				return []string{"--skip-ssl"}
			case "client-dump", "client-binlog":
				return []string{"--ssl=FALSE"}
			}
		}
	}

	return []string{}
}

func (server *ServerMonitor) GetStatusDeltaValue(name string) int {
	cur, err := strconv.Atoi(server.Status.Get(name))
	prev, err2 := strconv.Atoi(server.PrevStatus.Get(name))
	if err != nil || err2 != nil {
		return 0
	}
	return cur - prev
}

func (server *ServerMonitor) GetWorkingAgent() string {
	server.workingAgentMu.RLock()
	defer server.workingAgentMu.RUnlock()
	return server.WorkingAgent
}

func (server *ServerMonitor) JobsGetEntries() config.ServerTaskList {
	sTask := config.ServerTaskList{
		ServerURL: server.URL,
		Tasks:     make([]config.Task, 0),
	}

	server.JobResults.Range(func(k, v any) bool {
		sTask.Tasks = append(sTask.Tasks, *v.(*config.Task))
		return true
	})
	misc.SortByKey(sTask.Tasks, func(t config.Task) string { return t.Task })
	return sTask
}

func (server *ServerMonitor) IsInTask(taskname string) bool {
	task, exist := server.JobResults.CheckAndGet(taskname)
	if exist && task.Done == 0 {
		return true
	}

	return false
}

// ── PFS join-link enrichment ──────────────────────────────────────────────────

// ApplyPFSJoinLinksToSchema enriches a tablemap (keyed "schema.table") with
// join-weight links derived from the explain plans cached during the last PFS
// snapshot period.
//
// It is called once per snapshot cycle, right after FlushPFSSnapshotToLog,
// so the cache always reflects the period that just ended.
//
// The function is safe to call from the monitoring goroutine: it only reads
// PFSExplainCache (under its mutex) and PFSQueries (via ToNewMap snapshot),
// and writes only to the caller-supplied tablemap which is not shared.
func (server *ServerMonitor) ApplyPFSJoinLinksToSchema(tablemap map[string]*dbhelper.Table) {
	cluster := server.ClusterGroup

	// Collect all cached explain records — these are the plans for the
	// digests seen during the last snapshot period.
	cachedRecords := server.GetAllCachedExplainPFS()
	if len(cachedRecords) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg,
			"PFS join links: no cached explain records for %s — skipping", server.URL)
		return
	}

	// Convert cluster.PFSExplainRecord → dbhelper.PFSQueryPlan.
	// dbhelper cannot import cluster (cycle), so we project only the three
	// fields that LoadPFSJoinLinks actually reads.
	records := make([]dbhelper.PFSQueryPlan, 0, len(cachedRecords))
	for _, r := range cachedRecords {
		records = append(records, dbhelper.PFSQueryPlan{
			Digest:     r.Digest,
			SchemaName: r.SchemaName,
			Plan:       r.Plan,
		})
	}

	// Build execCount map from the live PFSQueriesMap.
	// ToNewMap returns a point-in-time snapshot — safe to read without locking.
	execCounts := make(map[string]int64, len(records))
	for digest, q := range server.PFSQueries.ToNewMap() {
		execCounts[digest] = q.Exec_count
	}

	dbhelper.LoadPFSJoinLinks(records, execCounts, tablemap)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"PFS join links: enriched table graph from %d explain records on %s", len(records), server.URL)
}
