// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
)

// IOPS calibration: sysbench fileio, random read/write with direct IO (no page cache),
// in replication-manager's working directory, the storage of the host it runs on. The
// container is not IO-limited, so it measures the host. resource-manager-infra-iops is the
// whole infrastructure: the answer gives the host's IOPS and that figure x the agents, the
// value applied when every node has the same storage.
const (
	iopsFileTotalSize = "2G"
	iopsRunSeconds    = 20
	iopsThreads       = 16
)

var (
	iopsCalibrationRunning    atomic.Bool
	errIOPSCalibrationRunning = errors.New("an IOPS calibration is already running")
	sysbenchReadsRe           = regexp.MustCompile(`reads/s:\s+([0-9.]+)`)
	sysbenchWritesRe          = regexp.MustCompile(`writes/s:\s+([0-9.]+)`)
)

// IOPSCalibration is the answer of the IOPS calibration.
type IOPSCalibration struct {
	ReadsPerSecond  float64 `json:"readsPerSecond"`
	WritesPerSecond float64 `json:"writesPerSecond"`
	HostIops        float64 `json:"hostIops"`
	Agents          int     `json:"agents"`
	InfraIops       float64 `json:"infraIops"`
	Dir             string  `json:"dir"`
	DurationSecond  int     `json:"durationSecond"`
	Applied         bool    `json:"applied"`
	Message         string  `json:"message"`
}

// parseFileioIops reads the read and write rates of a sysbench fileio run.
func parseFileioIops(out []byte) (reads, writes float64, err error) {
	r, w := sysbenchReadsRe.FindSubmatch(out), sysbenchWritesRe.FindSubmatch(out)
	if r == nil || w == nil {
		return 0, 0, errors.New("sysbench fileio: no reads/s or writes/s in its output")
	}
	reads, _ = strconv.ParseFloat(string(r[1]), 64)
	writes, _ = strconv.ParseFloat(string(w[1]), 64)
	return reads, writes, nil
}

// CalibrateIOPS measures the random IOPS of this host's storage and, when apply is set,
// writes host IOPS x agents to resource-manager-infra-iops.
func (repman *ReplicationManager) CalibrateIOPS(ctx context.Context, user, url string, apply bool) (*IOPSCalibration, error) {
	if !iopsCalibrationRunning.CompareAndSwap(false, true) {
		return nil, errIOPSCalibrationRunning
	}
	defer iopsCalibrationRunning.Store(false)
	start := time.Now()
	dir, err := os.MkdirTemp(repman.Conf.WorkingDir, ".iops-calibration-")
	if err != nil {
		return nil, fmt.Errorf("no scratch directory in %s: %w", repman.Conf.WorkingDir, err)
	}
	defer os.RemoveAll(dir) // the test files are never left behind (2G)
	files := []string{"fileio", "--file-total-size=" + iopsFileTotalSize, "--file-num=4"}
	if _, err := repman.sysbenchExec(ctx, dir, append(files, "prepare")...); err != nil {
		return nil, err
	}
	out, err := repman.sysbenchExec(ctx, dir, append(files, "--file-test-mode=rndrw", "--file-extra-flags=direct",
		"--file-fsync-freq=0", "--file-block-size=16384", "--threads="+strconv.Itoa(iopsThreads), "--time="+strconv.Itoa(iopsRunSeconds), "run")...)
	if err != nil {
		return nil, err
	}
	res := &IOPSCalibration{Dir: filepath.Dir(dir)}
	if res.ReadsPerSecond, res.WritesPerSecond, err = parseFileioIops(out); err != nil {
		return nil, err
	}
	res.HostIops = math.Round(res.ReadsPerSecond + res.WritesPerSecond)
	res.Agents = len(repman.infraCapacityInputs(repman.sortedClusters()).AgentCores) // the agents the ResourceManager page counts
	if res.Agents < 1 {
		res.Agents = 1
	}
	res.InfraIops = res.HostIops * float64(res.Agents)
	res.DurationSecond = int(time.Since(start).Seconds())
	res.Message = fmt.Sprintf("%.0f IOPS on this host (16 KiB random read/write, direct IO: %.0f reads/s + %.0f writes/s) x %d agent(s) = %.0f",
		res.HostIops, res.ReadsPerSecond, res.WritesPerSecond, res.Agents, res.InfraIops)
	if apply {
		if err := repman.setServerSetting(user, url, "resource-manager-infra-iops", strconv.FormatFloat(res.InfraIops, 'f', 0, 64)); err != nil {
			return res, fmt.Errorf("measured %s but not applied: %w", res.Message, err)
		}
		res.Applied = true
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "IOPS calibration: %s (applied %t)", res.Message, res.Applied)
	return res, nil
}

// handlerMuxCalibrateIOPS godoc
// @Summary Measure the random IOPS of the replication-manager host's storage with sysbench
// @Description Runs the embedded sysbench fileio (2G of files, 16 KiB random read/write, direct IO, 16 threads, 20 s) in the working directory at the lowest CPU priority, then removes the files. Answers the host IOPS and that figure times the agents and, with apply=true, writes the latter to resource-manager-infra-iops. Representative when every node has the same storage.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param apply query bool false "write resource-manager-infra-iops (default false)"
// @Success 200 {object} IOPSCalibration
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "An IOPS calibration is already running"
// @Router /api/clusters/settings/actions/calibrate-iops [post]
func (repman *ReplicationManager) handlerMuxCalibrateIOPS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var mycluster = repman.firstCluster()
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	valid, user := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	apply := r.URL.Query().Get("apply") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := repman.CalibrateIOPS(ctx, user, r.URL.Path, apply)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errIOPSCalibrationRunning) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
