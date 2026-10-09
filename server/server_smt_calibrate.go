// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// SMT gain calibration (#1958): the embedded sysbench measures, on the host
// replication-manager runs on, the throughput with one thread per physical core
// and with every SMT thread busy; the ratio is resource-manager-smt-gain. It is
// representative of the infrastructure when its nodes share the same hardware.

// SMTCalibration is the result of one calibration.
type SMTCalibration struct {
	Cores          int     `json:"cores"`
	Threads        int     `json:"threads"`
	CPUPerCore     float64 `json:"cpuEventsPerSecCores"`
	CPUPerThread   float64 `json:"cpuEventsPerSecThreads"`
	MemPerCore     float64 `json:"memMiBPerSecCores"`
	MemPerThread   float64 `json:"memMiBPerSecThreads"`
	CPUGain        float64 `json:"cpuGain"`
	MemGain        float64 `json:"memGain"`
	Gain           float64 `json:"gain"`
	Applied        bool    `json:"applied"`
	Message        string  `json:"message"`
	DurationSecond int     `json:"durationSeconds"`
}

var smtCalibrationRunning atomic.Bool

// smtRunSeconds is the length of each sysbench run (4 runs per calibration).
const smtRunSeconds = 15

// hostCoresAndThreads counts the physical cores (distinct physical id + core id in
// /proc/cpuinfo) and the logical CPUs of the host.
func hostCoresAndThreads(cpuinfo string) (cores, threads int) {
	seen := map[string]bool{}
	phys, core := "", ""
	flush := func() {
		if phys != "" || core != "" {
			seen[phys+"/"+core] = true
		}
		phys, core = "", ""
	}
	for _, line := range strings.Split(cpuinfo, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if strings.TrimSpace(line) == "" {
				flush()
			}
			continue
		}
		switch strings.TrimSpace(k) {
		case "processor":
			threads++
		case "physical id":
			phys = strings.TrimSpace(v)
		case "core id":
			core = strings.TrimSpace(v)
		}
	}
	flush()
	cores = len(seen)
	if cores == 0 || cores > threads {
		cores = threads // no topology in cpuinfo (some VMs): no SMT can be measured
	}
	return cores, threads
}

// containerCPUCapped reports a CPU quota on the replication-manager cgroup (v2
// cpu.max or v1 cfs quota): sysbench could not load every thread, the measure
// would be wrong.
func containerCPUCapped() bool {
	if b, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		return !strings.HasPrefix(strings.TrimSpace(string(b)), "max")
	}
	if b, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); err == nil {
		return strings.TrimSpace(string(b)) != "-1"
	}
	return false
}

var (
	sysbenchEventsRe = regexp.MustCompile(`events per second:\s*([0-9.]+)`)
	sysbenchMemRe    = regexp.MustCompile(`MiB transferred \(([0-9.]+) MiB/sec\)`)
)

func (repman *ReplicationManager) sysbenchRun(ctx context.Context, re *regexp.Regexp, args ...string) (float64, error) {
	bin := repman.Conf.SysbenchBinaryPath
	if _, err := os.Stat(bin); err != nil {
		if p, lerr := exec.LookPath("sysbench"); lerr == nil {
			bin = p
		} else {
			return 0, fmt.Errorf("sysbench not found (%s)", repman.Conf.SysbenchBinaryPath)
		}
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("sysbench %s: %v", strings.Join(args[:1], " "), err)
	}
	m := re.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("sysbench %s: no result in its output", args[0])
	}
	return strconv.ParseFloat(string(m[1]), 64)
}

// CalibrateSMTGain measures the SMT gain of this host and, when apply is set and
// the host runs SMT, writes it to resource-manager-smt-gain.
func (repman *ReplicationManager) CalibrateSMTGain(ctx context.Context, user, url string, apply bool) (*SMTCalibration, error) {
	if !smtCalibrationRunning.CompareAndSwap(false, true) {
		return nil, errors.New("a calibration is already running")
	}
	defer smtCalibrationRunning.Store(false)
	start := time.Now()
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	res := &SMTCalibration{}
	res.Cores, res.Threads = hostCoresAndThreads(string(b))
	if res.Threads <= res.Cores {
		res.Gain, res.Message = 1, fmt.Sprintf("no SMT on this host (%d cores, %d threads): nothing to calibrate, the gain stays %.2f", res.Cores, res.Threads, repman.Conf.ResourceManagerSmtGain)
		return res, nil
	}
	if containerCPUCapped() {
		return nil, errors.New("replication-manager runs under a CPU quota: sysbench cannot load every thread, the measure would be wrong")
	}
	dur := strconv.Itoa(smtRunSeconds)
	cpu := func(t int) (float64, error) {
		return repman.sysbenchRun(ctx, sysbenchEventsRe, "cpu", "--threads="+strconv.Itoa(t), "--time="+dur, "--cpu-max-prime=20000", "run")
	}
	mem := func(t int) (float64, error) {
		return repman.sysbenchRun(ctx, sysbenchMemRe, "memory", "--threads="+strconv.Itoa(t), "--time="+dur, "--memory-block-size=4K", "--memory-total-size=1000T", "--memory-access-mode=rnd", "run")
	}
	if res.CPUPerCore, err = cpu(res.Cores); err != nil {
		return nil, err
	}
	if res.CPUPerThread, err = cpu(res.Threads); err != nil {
		return nil, err
	}
	if res.MemPerCore, err = mem(res.Cores); err != nil {
		return nil, err
	}
	if res.MemPerThread, err = mem(res.Threads); err != nil {
		return nil, err
	}
	if res.CPUPerCore <= 0 || res.MemPerCore <= 0 {
		return nil, errors.New("sysbench returned no throughput")
	}
	res.CPUGain = res.CPUPerThread / res.CPUPerCore
	res.MemGain = res.MemPerThread / res.MemPerCore
	// a database sits between pure computation and memory waits: the mean of both,
	// at least 1, at most the threads per core
	g := (res.CPUGain + res.MemGain) / 2
	g = math.Max(1, math.Min(g, float64(res.Threads)/float64(res.Cores)))
	res.Gain = math.Round(g*100) / 100
	res.DurationSecond = int(time.Since(start).Seconds())
	res.Message = fmt.Sprintf("%d cores / %d threads: cpu x%.2f, memory x%.2f, gain %.2f", res.Cores, res.Threads, res.CPUGain, res.MemGain, res.Gain)
	if apply {
		if err := repman.setServerSetting(user, url, "resource-manager-smt-gain", strconv.FormatFloat(res.Gain, 'f', 2, 64)); err != nil {
			return res, fmt.Errorf("measured %s but not applied: %w", res.Message, err)
		}
		res.Applied = true
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SMT gain calibration: %s (applied %t)", res.Message, res.Applied)
	return res, nil
}

// handlerMuxCalibrateSMTGain godoc
// @Summary Measure the SMT (hyperthreading) gain of the replication-manager host with sysbench
// @Description Runs the embedded sysbench (cpu and random memory access, one thread per physical core then every thread, 15 s each) on the host replication-manager runs on, computes the gain and, unless apply=false, writes it to resource-manager-smt-gain. Representative when the infrastructure's nodes share the same hardware. A host without SMT is not an error: the answer says so and nothing is written.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param apply query bool false "write the measured gain (default true)"
// @Success 200 {object} SMTCalibration
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "A calibration is already running"
// @Failure 500 {string} string "Measure failed"
// @Router /api/clusters/settings/actions/calibrate-smt-gain [post]
func (repman *ReplicationManager) handlerMuxCalibrateSMTGain(w http.ResponseWriter, r *http.Request) {
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
	apply := r.URL.Query().Get("apply") != "false"
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := repman.CalibrateSMTGain(ctx, user, r.URL.Path, apply)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "already running") {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (repman *ReplicationManager) firstCluster() *cluster.Cluster {
	for _, v := range repman.Clusters {
		if v != nil {
			return v
		}
	}
	return nil
}
