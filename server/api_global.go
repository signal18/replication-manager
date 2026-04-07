// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
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
}

// globalLogsResponse is the JSON payload for GET /api/global/http-logs.
// It uses the same general→HttpLog shape as the cluster log endpoint so the
// frontend can reuse the existing Logs component without modification.
type globalLogsResponse struct {
	General globalLogsSnapshot `json:"general"`
}

// handlerMuxGlobalLogs returns server-level logs from repman.Logs.
//
// @Summary Get global logs
// @Description Returns server-level log entries from the ReplicationManager in-memory ring buffer.
// @Tags Global
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} globalLogsResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /api/global/http-logs [get]
func (repman *ReplicationManager) handlerMuxGlobalLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	repman.Logs.L.Lock()
	raw := make([]s18log.HttpMessage, len(repman.Logs.Buffer))
	copy(raw, repman.Logs.Buffer)
	logLen := repman.Logs.Len
	logLine := repman.Logs.Line
	repman.Logs.L.Unlock()

	buf := make([]s18log.HttpMessage, 0, len(raw))
	for _, msg := range raw {
		if msg.Timestamp != "" && msg.Group == s18log.GroupNone {
			buf = append(buf, msg)
		}
	}

	resp := globalLogsResponse{
		General: globalLogsSnapshot{
			Buffer: buf,
			Len:    logLen,
			Line:   logLine,
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

// globalAlertsResponse is the JSON payload for GET /api/global/alerts.
// It mirrors the cluster alert shape (errors/warnings) so the frontend
// can use the same rendering logic for both cluster and global alerts.
type globalAlertsResponse struct {
	Errors   []state.StateHttp `json:"errors"`
	Warnings []state.StateHttp `json:"warnings"`
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

	resp := globalAlertsResponse{
		Errors:   []state.StateHttp{},
		Warnings: []state.StateHttp{},
	}

	sm := repman.GetStateMachine()
	if sm != nil {
		if errs := sm.GetOpenErrors(); errs != nil {
			resp.Errors = errs
		}
		if warns := sm.GetOpenWarnings(); warns != nil {
			resp.Warnings = warns
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
