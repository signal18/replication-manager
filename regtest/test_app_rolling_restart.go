// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestAppRollingRestart (#2217): the restart of a flex app on OpenSVC v3 is rolling. The
// test takes the cluster's first provisioned flex app with at least two instances up,
// restarts it (no node) and samples the up instances every second: never fewer than N-1,
// all N up at the end, every node restarted once. It restarts that app's instances one by
// one, so it is not in ALL: run it by name on a cluster with a flex app.
func (regtest *RegTest) TestAppRollingRestart(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, format, args...)
	}
	svc := cl.OpenSVCConnect()
	if !svc.IsV3() {
		logf(config.LvlErr, "TEST app-rolling-restart: FAIL the orchestrator is not OpenSVC v3")
		return false
	}
	var app *cluster.App
	var nodes []string
	for _, a := range cl.Apps {
		if a == nil || a.AppConfig == nil || a.AppConfig.IsMonitorOnly() || !a.HasProvisionCookie() || cl.GetAppHATopology(a.AppConfig) != config.OpenSVCTopologyFlex {
			continue
		}
		if up, err := svc.GetServiceNodeFromStateV3(a.GetServiceName()); err == nil && len(up) >= 2 {
			app, nodes = a, up
			break
		}
	}
	if app == nil {
		logf(config.LvlErr, "TEST app-rolling-restart: FAIL no provisioned flex app with two instances up in %s", cl.Name)
		return false
	}
	n := len(nodes)
	logf("TEST", "app-rolling-restart: %s, %d instances up on %s", app.Name, n, strings.Join(nodes, ","))

	type result struct {
		done []string
		err  error
	}
	finished := make(chan result, 1)
	go func() {
		done, err := cl.OpenSVCRestartAppService(app, "", "")
		finished <- result{done, err}
	}()
	minUp := n
	var res result
	for running := true; running; {
		select {
		case res = <-finished:
			running = false
		case <-time.After(time.Second):
			if up, err := svc.GetServiceNodeFromStateV3(app.GetServiceName()); err == nil && len(up) < minUp {
				minUp = len(up)
			}
		}
	}
	if res.err != nil {
		logf(config.LvlErr, "TEST app-rolling-restart: FAIL the restart failed after %v: %s", res.done, res.err)
		return false
	}
	if len(res.done) != n {
		logf(config.LvlErr, "TEST app-rolling-restart: FAIL %d instances restarted (%v), want %d", len(res.done), res.done, n)
		return false
	}
	if minUp < n-1 {
		logf(config.LvlErr, "TEST app-rolling-restart: FAIL only %d of %d instances were up at one point", minUp, n)
		return false
	}
	up, err := svc.GetServiceNodeFromStateV3(app.GetServiceName())
	if err != nil || len(up) != n {
		logf(config.LvlErr, "TEST app-rolling-restart: FAIL %v up at the end (%v), want %d", up, err, n)
		return false
	}
	logf("TEST", "app-rolling-restart: %s restarted node by node (%s), never fewer than %d of %d up -- OK", app.Name, strings.Join(res.done, ","), minUp, n)
	return true
}
