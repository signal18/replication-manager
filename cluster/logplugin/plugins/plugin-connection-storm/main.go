// plugin-connection-storm detects connection pool saturation and locked/waiting
// threads before "Too many connections" hits.
//
// WARN0307 — raised when Sleep connections exceed sleep-ratio of total OR
// when threads are seen in lock-wait states.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	sleep-ratio-threshold  float  default: 0.60  — fraction of sleeping connections to trigger  (env: REPMAN_CONNECTION_STORM_SLEEP_RATIO_THRESHOLD)
//	lock-wait-count        int    default: 3     — threads in lock-wait state to trigger        (env: REPMAN_CONNECTION_STORM_LOCK_WAIT_COUNT)
//	min-connections        int    default: 10    — skip evaluation below this total             (env: REPMAN_CONNECTION_STORM_MIN_CONNECTIONS)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

var lockWaitStates = []string{
	"waiting for table metadata lock",
	"waiting for table lock",
	"locked",
	"waiting for lock",
	"system lock",
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	sleepRatio := wire.CfgFloat(req.Config, "sleep-ratio-threshold", wire.EnvFloat("REPMAN_CONNECTION_STORM_SLEEP_RATIO_THRESHOLD", 0.60))
	lockWaitThreshold := wire.CfgInt(req.Config, "lock-wait-count", wire.EnvInt("REPMAN_CONNECTION_STORM_LOCK_WAIT_COUNT", 3))
	minConns := wire.CfgInt(req.Config, "min-connections", wire.EnvInt("REPMAN_CONNECTION_STORM_MIN_CONNECTIONS", 10))

	total := len(req.ProcessList)
	if total < minConns {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	sleeping := 0
	lockWaiting := 0
	var lockWaiters []string

	for _, p := range req.ProcessList {
		if strings.EqualFold(p.Command, "sleep") {
			sleeping++
			continue
		}
		stateLower := strings.ToLower(p.State)
		for _, lws := range lockWaitStates {
			if strings.Contains(stateLower, lws) {
				lockWaiting++
				lockWaiters = append(lockWaiters, fmt.Sprintf(
					"%s@%s [%s] %.0fs", p.User, p.Host, p.State, p.TimeSeconds))
				break
			}
		}
	}

	var findings []wire.Finding

	ratio := float64(sleeping) / float64(total)
	if ratio >= sleepRatio {
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0307",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: connection pool saturation — %d/%d connections sleeping (%.0f%%), possible connection leak",
				req.ServerURL, sleeping, total, ratio*100),
		})
	}

	if lockWaiting >= lockWaitThreshold {
		var sample []string
		for i, w := range lockWaiters {
			if i >= 3 {
				break
			}
			sample = append(sample, w)
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0307",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: %d thread(s) waiting on locks: %s",
				req.ServerURL, lockWaiting, strings.Join(sample, " | ")),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}
