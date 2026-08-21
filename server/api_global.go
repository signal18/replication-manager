// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// processStartTime is set once at package init to compute uptime.
var processStartTime = time.Now()

// globalLogsSnapshot is a mutex-free snapshot of an HttpLog used for JSON encoding.
type globalLogsSnapshot struct {
	Buffer []s18log.HttpMessage `json:"buffer"`
	Len    int                  `json:"len"`
	Line   int                  `json:"line"`
	// Truncated is only ever true for a history response (see HttpLog.Truncated).
	Truncated bool `json:"truncated,omitempty"`
}

// globalLogsResponse is the JSON payload for GET /api/global/http-logs.
// It uses the same general→HttpLog shape as the cluster log endpoint so the
// frontend can reuse the existing Logs component without modification.
type globalLogsResponse struct {
	General globalLogsSnapshot `json:"general"`
}

// handlerMuxGlobalLogs returns server-level logs from repman.Logs.
//
// Plain requests return the in-memory ring buffer (fast, fixed cost — what
// the GUI polls). Adding ?since= and/or ?until= (RFC3339) switches to a
// bounded scan of the on-disk log file and its rotated backups instead — see
// doc/implementation/utils/s18log/LOG_HISTORY_READER.md — additionally
// filterable by ?level=, ?module=, ?text=, ?limit=. This is deliberately
// opt-in via since/until rather than a separate route: those params are
// never sent by the live-polling path, so the disk-scan cost can't leak onto
// the hot path (F2).
//
// @Summary Get global logs
// @Description Returns server-level log entries from the in-memory ring buffer, or — with ?since=/?until= — a bounded scan of on-disk log history.
// @Tags Global
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param since query string false "RFC3339 lower time bound; presence switches to on-disk history"
// @Param until query string false "RFC3339 upper time bound; presence switches to on-disk history"
// @Param level query string false "History mode only: comma-separated level buckets ERR,WARN,INFO,DBG"
// @Param module query string false "History mode only: comma-separated module tags, e.g. sql,proxy"
// @Param text query string false "History mode only: substring filter on message text"
// @Param limit query int false "History mode only: max lines returned (server-clamped)"
// @Success 200 {object} globalLogsResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Router /api/global/http-logs [get]
func (repman *ReplicationManager) handlerMuxGlobalLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !repman.UserHasGlobalGrant(r, config.GrantGlobalAdminShow) {
		http.Error(w, "Forbidden: requires "+config.GrantGlobalAdminShow+" grant", http.StatusForbidden)
		return
	}

	var buf []s18log.HttpMessage
	var truncated bool
	// bufLen/bufLine default to the live ring buffer's fixed capacity/cursor
	// (unchanged, existing meaning for the live path); the history branch
	// below overrides bufLen to the actual returned count, matching what
	// handlerMuxWebLog's HttpLog{Len: len(msgs)} already does for per-cluster
	// history — both history responses should mean the same thing by "len".
	bufLen := repman.GlobalLogs.Len
	bufLine := repman.GlobalLogs.Line
	if isLogHistoryRequest(r) {
		if !repman.Conf.LogHistoryEnable {
			http.Error(w, "Log history is disabled (log-history-enable=false)", http.StatusForbidden)
			return
		}
		// Group: GroupNone, not "" (no filter) — the live path this mirrors
		// (repman.GlobalLogs, populated only by server/server_log.go's
		// LogModuleWithFieldsPrintf with Group: "none") is server-only.
		// "" would additionally return every cluster's history rows, which
		// the live /api/global/http-logs response never does.
		msgs, tr, err := repman.readLogHistory(r, s18log.GroupNone, "")
		if err != nil {
			if errors.Is(err, errInvalidLogHistoryRange) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "Error reading log history: "+err.Error(), http.StatusInternalServerError)
			return
		}
		buf = msgs
		truncated = tr
		bufLen = len(msgs)
		bufLine = 0
	} else {
		repman.GlobalLogs.L.Lock()
		raw := make([]s18log.HttpMessage, len(repman.GlobalLogs.Buffer))
		copy(raw, repman.GlobalLogs.Buffer)
		repman.GlobalLogs.L.Unlock()

		buf = make([]s18log.HttpMessage, 0, len(raw))
		for _, msg := range raw {
			if msg.Timestamp != "" {
				buf = append(buf, msg)
			}
		}
	}

	resp := globalLogsResponse{
		General: globalLogsSnapshot{
			Buffer:    buf,
			Len:       bufLen,
			Line:      bufLine,
			Truncated: truncated,
		},
	}

	out, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// isLogHistoryRequest reports whether r asks for on-disk log history rather
// than the live in-memory buffer — see handlerMuxGlobalLogs / handlerMuxWebLog.
func isLogHistoryRequest(r *http.Request) bool {
	q := r.URL.Query()
	return q.Get("since") != "" || q.Get("until") != ""
}

// errInvalidLogHistoryRange is returned by readLogHistory when since/until is
// present but not valid RFC3339 — callers map it to a 400 rather than
// treating the unparsed value as "no bound" (which would silently widen the
// scan instead of rejecting the request).
var errInvalidLogHistoryRange = errors.New("since/until must be RFC3339 (e.g. 2006-01-02T15:04:05Z)")

// readLogHistory runs a bounded scan of the on-disk log file (and its
// rotated backups) via s18log.ReadHistoryFiles, using the request's
// level/module/text/since/until/limit query params (parsed the same way
// regardless of which endpoint called this). group scopes results to one
// cluster ("" = no filter across cluster tags — callers that need the
// server-only view must pass s18log.GroupNone explicitly, see
// handlerMuxGlobalLogs). logType, when "general" or "task", additionally
// restricts to the same general/task split the live per-cluster buffers use
// (config.IsTaskLogModule) — passed through as HistoryQuery.TaskSplit so the
// reader applies it before its Limit cutoff, not after (post-filtering an
// already limit-capped result could return far fewer than Limit task rows
// when general-log volume dominates the scanned window).
//
// Scans both repman.Conf.LogFile and its "-maintenance" sibling
// (maintenanceLogPath): cluster.LogModuleWithFieldsPrintf routes
// maintenance-adjacent modules (ConstLogModMaintenance/Task/Restic/SST/
// BackupStream/Purge) to a dedicated MaintenanceLogrus logger that writes
// the maintenance file instead of the main one, and both the "general" and
// "task" splits straddle that boundary — see s18log.ReadHistoryFiles and
// doc/implementation/utils/s18log/LOG_HISTORY_READER.md.
func (repman *ReplicationManager) readLogHistory(r *http.Request, group, logType string) ([]s18log.HttpMessage, bool, error) {
	q := r.URL.Query()

	levels := map[string]bool{}
	if raw := q.Get("level"); raw != "" {
		for _, l := range strings.Split(raw, ",") {
			if l = strings.ToUpper(strings.TrimSpace(l)); l != "" {
				levels[l] = true
			}
		}
	}

	modules := map[int]bool{}
	if raw := q.Get("module"); raw != "" {
		for _, m := range strings.Split(raw, ",") {
			if m = strings.TrimSpace(m); m != "" {
				modules[config.ModuleFromTag(m)] = true
			}
		}
	}

	// LogHistoryMaxLines 0 means "use the package default" (T18 — see
	// HistoryQuery's doc comment, and now reachable in practice via
	// /actions/clear/{settingName}, not just an unconfigured field) — resolve
	// that before using it as the clamp ceiling below, or a 0 config makes
	// `n < limit` false for every positive n and silently ignores the
	// caller's ?limit=, always falling back to the (much larger) default
	// instead of a smaller requested page size.
	maxLines := repman.Conf.LogHistoryMaxLines
	if maxLines <= 0 {
		maxLines = s18log.DefaultHistoryMaxLines
	}
	limit := maxLines
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < limit {
			limit = n
		}
	}

	var since, until time.Time
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, false, errInvalidLogHistoryRange
		}
		since = t
	}
	if raw := q.Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, false, errInvalidLogHistoryRange
		}
		until = t
	}

	files := []string{repman.Conf.LogFile}
	if repman.Conf.LogFile != "" {
		files = append(files, maintenanceLogPath(repman.Conf.LogFile))
	}

	result, err := s18log.ReadHistoryFiles(files, s18log.HistoryQuery{
		Group:        group,
		Levels:       levels,
		Modules:      modules,
		TaskSplit:    logType,
		Text:         q.Get("text"),
		Since:        since,
		Until:        until,
		Limit:        limit,
		MaxScanBytes: int64(repman.Conf.LogHistoryMaxScanBytes),
		MaxFiles:     repman.Conf.LogHistoryMaxFiles,
	})
	if err != nil {
		return nil, false, err
	}
	return result.Messages, result.Truncated, nil
}

// globalAlertsResponse is the JSON payload for GET /api/global/alerts.
// It mirrors the cluster alert shape (errors/warnings) so the frontend
// can use the same rendering logic for both cluster and global alerts.
type globalAlertsResponse struct {
	Errors   []state.StateHttp `json:"errors"`
	Warnings []state.StateHttp `json:"warnings"`
	Infos    []state.StateHttp `json:"infos"`
}

// handlerMuxGlobalAlerts returns the open errors and warnings from the
// ReplicationManager global state machine.
//
// @Summary Get global alerts
// @Description Returns server-level errors and warnings from the ReplicationManager state machine.
// @Tags Global
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} globalAlertsResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /api/global/alerts [get]
func (repman *ReplicationManager) handlerMuxGlobalAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !repman.UserHasGlobalGrant(r, config.GrantGlobalAdminShow) {
		http.Error(w, "Forbidden: requires "+config.GrantGlobalAdminShow+" grant", http.StatusForbidden)
		return
	}

	resp := globalAlertsResponse{
		Errors:   []state.StateHttp{},
		Warnings: []state.StateHttp{},
		Infos:    []state.StateHttp{},
	}

	sm := repman.GetStateMachine()
	if sm != nil {
		if errs := sm.GetOpenErrors(); errs != nil {
			resp.Errors = errs
		}
		if warns := sm.GetOpenWarnings(); warns != nil {
			resp.Warnings = warns
		}
		if infos := sm.GetOpenInfos(); infos != nil {
			resp.Infos = infos
		}
	}

	out, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// globalMetricsHostInfo holds host-level telemetry.
type globalMetricsHostInfo struct {
	Hostname          string  `json:"hostname"`
	CPUCores          int     `json:"cpuCores"`
	CPUPercent        float64 `json:"cpuPercent"`
	MemoryTotalBytes  uint64  `json:"memoryTotalBytes"`
	MemoryUsedBytes   uint64  `json:"memoryUsedBytes"`
	MemoryUsedPercent float64 `json:"memoryUsedPercent"`
	DiskPath          string  `json:"diskPath"`
	DiskTotalBytes    uint64  `json:"diskTotalBytes"`
	DiskUsedBytes     uint64  `json:"diskUsedBytes"`
	DiskUsedPercent   float64 `json:"diskUsedPercent"`
	DiskError         string  `json:"diskError,omitempty"`
}

// globalMetricsProcessInfo holds repman process-level telemetry.
type globalMetricsProcessInfo struct {
	PID            int    `json:"pid"`
	RSSBytes       uint64 `json:"rssBytes"`
	HeapAllocBytes uint64 `json:"heapAllocBytes"`
	HeapSysBytes   uint64 `json:"heapSysBytes"`
	Goroutines     int    `json:"goroutines"`
	UptimeSeconds  int64  `json:"uptimeSeconds"`
}

// globalMetricsResponse is the JSON payload for GET /api/global/metrics.
type globalMetricsResponse struct {
	Host    globalMetricsHostInfo    `json:"host"`
	Process globalMetricsProcessInfo `json:"process"`
}

// handlerMuxGlobalMetrics returns host and process telemetry for the running repman instance.
//
// @Summary Get global metrics
// @Description Returns host CPU/memory/disk and repman process metrics.
// @Tags Global
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} globalMetricsResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /api/global/metrics [get]
func (repman *ReplicationManager) handlerMuxGlobalMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !repman.UserHasGlobalGrant(r, config.GrantGlobalAdminShow) {
		http.Error(w, "Forbidden: requires "+config.GrantGlobalAdminShow+" grant", http.StatusForbidden)
		return
	}

	resp := globalMetricsResponse{}

	// --- Host metrics ---
	hostname, _ := os.Hostname()
	resp.Host.Hostname = hostname

	if cores, err := cpu.Counts(true); err == nil {
		resp.Host.CPUCores = cores
	}

	// Use non-blocking CPU percent to avoid handler latency on each poll.
	if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
		resp.Host.CPUPercent = percents[0]
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		resp.Host.MemoryTotalBytes = vm.Total
		resp.Host.MemoryUsedBytes = vm.Used
		resp.Host.MemoryUsedPercent = vm.UsedPercent
	}

	diskPath := repman.Conf.WorkingDir
	resp.Host.DiskPath = diskPath
	if diskPath != "" {
		if du, err := disk.Usage(diskPath); err == nil {
			resp.Host.DiskTotalBytes = du.Total
			resp.Host.DiskUsedBytes = du.Used
			resp.Host.DiskUsedPercent = du.UsedPercent
		} else {
			resp.Host.DiskError = err.Error()
		}
	} else {
		resp.Host.DiskError = "WorkingDir is not configured"
	}

	// --- Process metrics ---
	resp.Process.PID = os.Getpid()
	resp.Process.UptimeSeconds = int64(time.Since(processStartTime).Seconds())
	resp.Process.Goroutines = runtime.NumGoroutine()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	resp.Process.HeapAllocBytes = ms.HeapAlloc
	resp.Process.HeapSysBytes = ms.HeapSys

	if p, err := process.NewProcess(int32(resp.Process.PID)); err == nil {
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			resp.Process.RSSBytes = mi.RSS
		}
	}

	out, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// handlerMuxForgetArbitration resets all arbitration data by calling the
// arbitrator's /forget/ endpoint with the configured secret.
//
// @Summary Reset arbitration data
// @Description Deletes all heartbeat rows from the arbitrator for the configured secret.
// @Tags Global
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {string} string "OK"
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/actions/forget-arbitration [post]
func (repman *ReplicationManager) handlerMuxForgetArbitration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !repman.UserHasGlobalGrant(r, config.GrantGlobalAdminShow) {
		http.Error(w, "Forbidden: requires "+config.GrantGlobalAdminShow+" grant", http.StatusForbidden)
		return
	}

	secret := repman.Conf.ArbitrationSasSecret
	if secret == "" {
		http.Error(w, "arbitration-sas-secret is not configured", http.StatusBadRequest)
		return
	}

	arbHost := repman.Conf.ArbitrationSasHosts
	if arbHost == "" {
		http.Error(w, "arbitration-sas-hosts is not configured", http.StatusBadRequest)
		return
	}

	url := arbHost + "/forget/"
	if len(url) > 0 && url[0] != 'h' {
		url = "http://" + url
	}

	jsonStr := []byte(`{"secret":"` + secret + `"}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		http.Error(w, "Failed to create request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to reach arbitrator: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// globalJobsRecentLimit caps how many recently completed DB jobs are kept per cluster.
const globalJobsRecentLimit = 5

// globalRequestIdentity is the JWT-derived caller identity used to evaluate
// per-cluster ACL for /api/global/jobs sections.
type globalRequestIdentity struct {
	Username   string
	Password   string
	AuthMethod string
}

// resolveGlobalRequestIdentity parses the request JWT once so the same
// (username, password, authMethod) can be checked against multiple clusters'
// cluster.IsValidACL with synthetic per-section URLs. It mirrors the claims
// handling in IsValidClusterACL, kept local here rather than factored into that
// shared, widely-used function to avoid touching behavior other endpoints rely on.
func (repman *ReplicationManager) resolveGlobalRequestIdentity(r *http.Request) (globalRequestIdentity, bool) {
	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err != nil {
		return globalRequestIdentity{}, false
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return globalRequestIdentity{}, false
	}
	mycutinfo, ok := claims["CustomUserInfo"].(map[string]interface{})
	if !ok {
		return globalRequestIdentity{}, false
	}
	username, _ := mycutinfo["Name"].(string)
	password, _ := mycutinfo["Password"].(string)

	if profile, ok := mycutinfo["profile"].(string); ok {
		if strings.Contains(profile, repman.Conf.OAuthProvider) {
			email, _ := mycutinfo["email"].(string)
			return globalRequestIdentity{Username: email, Password: password, AuthMethod: "oidc"}, true
		}
	}
	return globalRequestIdentity{Username: username, Password: password, AuthMethod: "password"}, true
}

// globalJobEntry is a single DB job entry surfaced in the global jobs aggregate,
// annotated with the cluster and server it belongs to.
type globalJobEntry struct {
	ClusterName string `json:"clusterName"`
	ServerId    string `json:"serverId"`
	ServerUrl   string `json:"serverUrl"`
	Task        string `json:"task"`
	State       int    `json:"state"`
	StateLabel  string `json:"stateLabel"`
	Result      string `json:"result,omitempty"`
	Start       int64  `json:"start"`
	End         int64  `json:"end,omitempty"`
}

// globalClusterResticTask is the current restic task for one cluster in the global jobs aggregate.
type globalClusterResticTask struct {
	ClusterName string                     `json:"clusterName"`
	CurrentTask *backupmgr.ResticTaskState `json:"currentTask"`
}

// globalJobsResponse is the JSON payload for GET /api/global/jobs.
type globalJobsResponse struct {
	RunningJobs         []globalJobEntry          `json:"runningJobs"`
	RecentCompletedJobs []globalJobEntry          `json:"recentCompletedJobs"`
	ResticCurrentTasks  []globalClusterResticTask `json:"resticCurrentTasks"`
}

// jobStateLabel mirrors the state labels used by the per-cluster jobs UI
// (share/dashboard_react/src/Pages/Maintenance/DatabaseJobs).
func jobStateLabel(jobState int) string {
	switch jobState {
	case cluster.JobStateAvailable:
		return "Init"
	case cluster.JobStateRunning:
		return "Running"
	case cluster.JobStateHalted:
		return "Halted"
	case cluster.JobStateFinished:
		return "Done"
	case cluster.JobStateSuccess:
		return "Success"
	case cluster.JobStateErrorExec:
		return "Error"
	case cluster.JobStateErrorAfter:
		return "PTError"
	default:
		return "Unknown"
	}
}

// handlerMuxGlobalJobs returns a cross-cluster aggregate of running DB jobs, the
// last few completed DB jobs per cluster, and the current Restic task per cluster.
//
// @Summary Get global jobs
// @Description Returns running and recently completed DB jobs, and current Restic tasks, across all clusters.
// @Tags Global
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} globalJobsResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /api/global/jobs [get]
func (repman *ReplicationManager) handlerMuxGlobalJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !repman.UserHasGlobalGrant(r, config.GrantGlobalAdminShow) {
		http.Error(w, "Forbidden: requires "+config.GrantGlobalAdminShow+" grant", http.StatusForbidden)
		return
	}

	resp := globalJobsResponse{
		RunningJobs:         []globalJobEntry{},
		RecentCompletedJobs: []globalJobEntry{},
		ResticCurrentTasks:  []globalClusterResticTask{},
	}

	identity, ok := repman.resolveGlobalRequestIdentity(r)
	if !ok {
		http.Error(w, "Forbidden: unable to resolve caller identity", http.StatusForbidden)
		return
	}

	// Snapshot the Clusters map under the repman lock to avoid racing with
	// StartCluster / cluster removal, which mutate the map under that same lock.
	// This endpoint is polled continuously, so an unsynchronized range here would
	// eventually overlap a dynamic add/remove and panic (concurrent map read/write).
	repman.Lock()
	clusterSnapshot := make(map[string]*cluster.Cluster, len(repman.Clusters))
	for k, v := range repman.Clusters {
		clusterSnapshot[k] = v
	}
	repman.Unlock()

	clusterNames := make([]string, 0, len(clusterSnapshot))
	for name := range clusterSnapshot {
		clusterNames = append(clusterNames, name)
	}
	sort.Strings(clusterNames)

	for _, name := range clusterNames {
		cl := clusterSnapshot[name]
		if cl == nil {
			continue
		}

		// Per-cluster ACL: only include a section if this caller would already be
		// allowed to reach the equivalent per-cluster endpoint. global-admin-show
		// grants visibility into the aggregate, not into every cluster's data.
		// Quiet variant: this endpoint is polled and probes every cluster, so a
		// caller without access to cluster X would otherwise log a denial on X
		// every refresh even though the denial here is expected, not an incident.
		canViewJobs := cl.IsValidACLQuiet(identity.Username, identity.Password, "/api/clusters/"+name+"/jobs", identity.AuthMethod)
		canViewRestic := cl.IsValidACLQuiet(identity.Username, identity.Password, "/api/clusters/"+name+"/restic/task-current", identity.AuthMethod)

		if canViewJobs {
			if entries, err := cl.JobsGetEntries(); err == nil {
				var completed []globalJobEntry
				for serverId, list := range entries.Servers {
					for _, t := range list.Tasks {
						entry := globalJobEntry{
							ClusterName: name,
							ServerId:    serverId,
							ServerUrl:   list.ServerURL,
							Task:        t.Task,
							State:       t.State,
							StateLabel:  jobStateLabel(t.State),
							Result:      t.Result,
							Start:       t.Start,
							End:         t.End,
						}
						switch t.State {
						case cluster.JobStateAvailable, cluster.JobStateRunning, cluster.JobStateHalted:
							resp.RunningJobs = append(resp.RunningJobs, entry)
						case cluster.JobStateFinished, cluster.JobStateSuccess, cluster.JobStateErrorExec, cluster.JobStateErrorAfter:
							completed = append(completed, entry)
						}
					}
				}
				sort.Slice(completed, func(i, j int) bool { return completed[i].End > completed[j].End })
				if len(completed) > globalJobsRecentLimit {
					completed = completed[:globalJobsRecentLimit]
				}
				resp.RecentCompletedJobs = append(resp.RecentCompletedJobs, completed...)
			}
		}

		if canViewRestic && cl.ResticManager != nil {
			if task := cl.ResticManager.GetCurrentTaskState(); task != nil {
				resp.ResticCurrentTasks = append(resp.ResticCurrentTasks, globalClusterResticTask{
					ClusterName: name,
					CurrentTask: task,
				})
			}
		}
	}

	// Per-cluster capping above keeps any one cluster from crowding out the rest;
	// this final sort makes the combined list globally newest-first.
	sort.Slice(resp.RecentCompletedJobs, func(i, j int) bool {
		return resp.RecentCompletedJobs[i].End > resp.RecentCompletedJobs[j].End
	})

	// entries.Servers (cluster/cluster_job.go) is a map, so the per-server
	// iteration order that built RunningJobs is randomized per request; sort it
	// for a stable row order across polls even when nothing has changed.
	sort.Slice(resp.RunningJobs, func(i, j int) bool {
		a, b := resp.RunningJobs[i], resp.RunningJobs[j]
		if a.ClusterName != b.ClusterName {
			return a.ClusterName < b.ClusterName
		}
		if a.ServerUrl != b.ServerUrl {
			return a.ServerUrl < b.ServerUrl
		}
		return a.Task < b.Task
	})

	out, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}
