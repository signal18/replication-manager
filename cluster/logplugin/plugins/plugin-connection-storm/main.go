// plugin-connection-storm detects connection pool saturation and locked/waiting
// threads before "Too many connections" hits.
//
// WARN0307 — raised when Sleep connections exceed sleep-ratio of total OR
// when threads are seen in lock-wait states.
//
// Config (environment variables):
//
//	REPMAN_SLEEP_RATIO_THRESHOLD float  default: 0.60  — 60% sleeping
//	REPMAN_LOCK_WAIT_COUNT       int    default: 3     — threads in lock-wait state
//	REPMAN_MIN_CONNECTIONS       int    default: 10    — minimum total to trigger
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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

	sleepRatio := envFloat("REPMAN_SLEEP_RATIO_THRESHOLD", 0.60)
	lockWaitThreshold := envInt("REPMAN_LOCK_WAIT_COUNT", 3)
	minConns := envInt("REPMAN_MIN_CONNECTIONS", 10)

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

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
