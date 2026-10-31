// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Group replication (multi-master-grprep, MySQL and Percona): the group elects its PRIMARY,
// replication-manager follows it (its master is the PRIMARY, the other members its
// replicas), makes another member PRIMARY on a switchover and leaves a lost PRIMARY to the
// group's own election. These tests read the group live on every member
// (performance_schema.replication_group_members, through the dbhelper reads the monitor
// uses) and check replication-manager's view against it (F5). FUNCTIONAL: their targets
// (testTargets) are the topology, never the orchestrator.

// groupReplicationMembers reads the group on each server: the ONLINE PRIMARY members and
// the ONLINE SECONDARY ones. A server that answers neither is not an online member.
func groupReplicationMembers(cl *cluster.Cluster) (primaries, secondaries []*cluster.ServerMonitor) {
	for _, s := range cl.Servers {
		if s.Conn == nil {
			continue
		}
		if ok, err := dbhelper.IsGroupReplicationMaster(s.Conn, s.DBVersion, s.Host); err == nil && ok {
			primaries = append(primaries, s)
			continue
		}
		if ok, err := dbhelper.IsGroupReplicationSlave(s.Conn, s.DBVersion, s.Host); err == nil && ok {
			secondaries = append(secondaries, s)
		}
	}
	return primaries, secondaries
}

// groupReplicationHealthy says whether every server is an ONLINE member, exactly one of
// them PRIMARY, and replication-manager's master is that PRIMARY; else why not.
func groupReplicationHealthy(cl *cluster.Cluster) (*cluster.ServerMonitor, error) {
	primaries, secondaries := groupReplicationMembers(cl)
	if len(primaries) != 1 {
		return nil, fmt.Errorf("%d ONLINE PRIMARY members (want 1)", len(primaries))
	}
	if online := len(primaries) + len(secondaries); online != len(cl.Servers) {
		return primaries[0], fmt.Errorf("%d of %d servers are ONLINE members", online, len(cl.Servers))
	}
	if m := cl.GetMaster(); m == nil || m.URL != primaries[0].URL {
		got := "none"
		if m != nil {
			got = m.URL
		}
		return primaries[0], fmt.Errorf("replication-manager's master is %s, the group's PRIMARY is %s", got, primaries[0].URL)
	}
	return primaries[0], nil
}

// groupReplicationProbeEverywhere writes the probe table on the PRIMARY and waits until
// every member has it (the group certifies and applies it on each).
func groupReplicationProbeEverywhere(cl *cluster.Cluster, primary *cluster.ServerMonitor, timeout time.Duration) error {
	if err := createProbeTable(primary); err != nil {
		return err
	}
	var missing []string
	if !waitFor(timeout, func() bool {
		missing = missing[:0]
		for _, s := range cl.Servers {
			if ok, err := probeTablePresent(s); err != nil || !ok {
				missing = append(missing, s.URL)
			}
		}
		return len(missing) == 0
	}) {
		return fmt.Errorf("probe table %s.%s missing on %s after %s", rollingProbeSchema, rollingProbeTable, strings.Join(missing, ", "), timeout)
	}
	return nil
}

// TestGroupReplicationBootstrap checks a bootstrapped group: every server an ONLINE member,
// one PRIMARY, replication-manager's master that PRIMARY, and a write on it reaching every
// member.
func (regtest *RegTest) TestGroupReplicationBootstrap(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	var primary *cluster.ServerMonitor
	var err error
	// the monitor needs a tick or two to read the group after a provision or a restart
	if !waitFor(30*time.Second, func() bool { primary, err = groupReplicationHealthy(cl); return err == nil }) {
		logf("FAIL GroupReplicationBootstrap: %s", err)
		return false
	}
	defer dropProbeTable(cl)
	if err := groupReplicationProbeEverywhere(cl, primary, 30*time.Second); err != nil {
		logf("FAIL GroupReplicationBootstrap: %s", err)
		return false
	}
	logf("PASS GroupReplicationBootstrap: %d ONLINE members, PRIMARY %s is replication-manager's master, writes reach every member", len(cl.Servers), primary.URL)
	return true
}

// TestGroupReplicationSwitchover makes another member PRIMARY through replication-manager's
// switchover and checks the group agrees: a new PRIMARY, which is replication-manager's
// master, the old one an ONLINE SECONDARY, and writes on the new PRIMARY everywhere.
func (regtest *RegTest) TestGroupReplicationSwitchover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	before, err := groupReplicationHealthy(cl)
	if err != nil {
		logf("FAIL GroupReplicationSwitchover: the group is not healthy before the switchover: %s", err)
		return false
	}
	cl.SwitchoverWaitTest()

	var after *cluster.ServerMonitor
	if !waitFor(60*time.Second, func() bool {
		after, err = groupReplicationHealthy(cl)
		return err == nil && after.URL != before.URL
	}) {
		if err == nil {
			err = fmt.Errorf("the PRIMARY is still %s", before.URL)
		}
		logf("FAIL GroupReplicationSwitchover: %s", err)
		return false
	}
	defer dropProbeTable(cl)
	if err := groupReplicationProbeEverywhere(cl, after, 30*time.Second); err != nil {
		logf("FAIL GroupReplicationSwitchover: %s", err)
		return false
	}
	logf("PASS GroupReplicationSwitchover: PRIMARY %s -> %s, the old PRIMARY is an ONLINE SECONDARY, writes reach every member", before.URL, after.URL)
	return true
}

// TestGroupReplicationPrimaryLoss stops the PRIMARY's database service: the group elects a
// new PRIMARY, replication-manager follows it, and once started again the old PRIMARY is an
// ONLINE SECONDARY with the writes made while it was down.
func (regtest *RegTest) TestGroupReplicationPrimaryLoss(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	lost, err := groupReplicationHealthy(cl)
	if err != nil {
		logf("FAIL GroupReplicationPrimaryLoss: the group is not healthy before the test: %s", err)
		return false
	}
	logf("Stopping the PRIMARY %s", lost.URL)
	if err := cl.StopDatabaseService(lost); err != nil {
		logf("FAIL GroupReplicationPrimaryLoss: cannot stop %s: %s", lost.URL, err)
		return false
	}

	// the remaining members elect a PRIMARY among themselves, replication-manager follows
	var elected *cluster.ServerMonitor
	if !waitFor(120*time.Second, func() bool {
		primaries, _ := groupReplicationMembers(cl)
		if len(primaries) != 1 || primaries[0].URL == lost.URL {
			return false
		}
		elected = primaries[0]
		m := cl.GetMaster()
		return m != nil && m.URL == elected.URL
	}) {
		got := "none"
		if m := cl.GetMaster(); m != nil {
			got = m.URL
		}
		logf("FAIL GroupReplicationPrimaryLoss: no new PRIMARY followed by replication-manager 120s after stopping %s (elected %v, replication-manager's master %s)", lost.URL, elected != nil, got)
		cl.StartDatabaseService(lost)
		return false
	}
	defer dropProbeTable(cl)
	// a write while the old PRIMARY is down, which it must get when it comes back
	if err := createProbeTable(elected); err != nil {
		logf("FAIL GroupReplicationPrimaryLoss: %s", err)
		cl.StartDatabaseService(lost)
		return false
	}

	logf("Starting %s again", lost.URL)
	if err := cl.StartDatabaseService(lost); err != nil {
		logf("FAIL GroupReplicationPrimaryLoss: cannot start %s: %s", lost.URL, err)
		return false
	}
	if !waitFor(300*time.Second, func() bool {
		_, secondaries := groupReplicationMembers(cl)
		for _, s := range secondaries {
			if s.URL == lost.URL {
				return true
			}
		}
		return false
	}) {
		logf("FAIL GroupReplicationPrimaryLoss: %s is not an ONLINE SECONDARY 300s after its start", lost.URL)
		return false
	}
	if err := groupReplicationProbeEverywhere(cl, elected, 60*time.Second); err != nil {
		logf("FAIL GroupReplicationPrimaryLoss: %s", err)
		return false
	}
	if _, err := groupReplicationHealthy(cl); err != nil {
		logf("FAIL GroupReplicationPrimaryLoss: %s", err)
		return false
	}
	logf("PASS GroupReplicationPrimaryLoss: %s lost, the group elected %s, replication-manager followed, %s rejoined as an ONLINE SECONDARY with the writes", lost.URL, elected.URL, lost.URL)
	return true
}
