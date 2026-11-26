// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// The host step of the rolling upgrade (doc/implementation/cluster/ORCHESTRATOR_INTERFACE.md,
// section 10.4): on a backend that drives the hosts (Traits().HasHost, the proxmox backend)
// and with prov-db-rolling-upgrade-host on, each server's rolling upgrade runs, between the
// stop of its database and the upgrade of its packages:
//
//	prov-db-host-upgrade-script in the guest (the client's hook: kernel and packages, F6)
//	orch.RebootHost(server)          the VM, through the hypervisor
//	wait for the VM running and its SSH port answering, bounded by prov-db-host-reboot-timeout
//
// then the database upgrade goes on as without the step. The step is off by default (T14).

// hostWaitInterval is the poll interval of the wait for a rebooted host; a variable for the tests.
var hostWaitInterval = 2 * time.Second

// hostGuestReachable tells whether the guest answers on its SSH port; a variable for the tests.
var hostGuestReachable = func(address string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// rollingUpgradeHostStep runs the host step for one server whose database is stopped. It
// returns nil when the step does not apply (switched off, a backend without hosts, a server
// without a VM: the per-tick check CheckRollingUpgradeHost reports the last two as states).
// An error stops the rolling upgrade at this server, as any failed step.
func (cluster *Cluster) rollingUpgradeHostStep(server *ServerMonitor) error {
	if cluster.Conf == nil || !cluster.Conf.ProvDbRollingUpgradeHost || server == nil {
		return nil
	}
	orch := cluster.orchestrator(server)
	if !orch.Traits().HasHost {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Rolling upgrade: host step skipped on %s: orchestrator %s (%s) does not drive the hosts", server.URL, orch.Name(), orch.Type())
		return nil
	}
	vm, ok, err := cluster.hostVM(server)
	if err != nil {
		return err
	} else if !ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Rolling upgrade: host step skipped on %s: no VM in prov-db-host-vmids", server.URL)
		return nil
	}
	if script := strings.TrimSpace(cluster.Conf.ProvDbHostUpgradeScript); script != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Rolling upgrade: running host upgrade script %s on %s", script, server.URL)
		out, errOut, err := cluster.runGuestScript(server, script)
		if err != nil {
			return fmt.Errorf("host upgrade script %s on %s: %w %s", script, server.URL, err, errOut)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Rolling upgrade: host upgrade script %s on %s, out: %s, err: %s", script, server.URL, out, errOut)
	}
	// one deadline for the reboot and the return of the host: prov-db-host-reboot-timeout
	// bounds the whole step after the script, not each of its phases
	deadline := time.Now().Add(cluster.hostRebootTimeout())
	cluster.hostStepDeadlines.Store(server.URL, deadline)
	defer cluster.hostStepDeadlines.Delete(server.URL)
	if err := orch.RebootHost(server); err != nil {
		return fmt.Errorf("reboot of the VM %s/%s of %s failed, the VM may be stopped or mid-reboot (check it in Proxmox, then run the rolling upgrade again): %w",
			vm.Node, vm.VMID, server.URL, err)
	}
	if err := cluster.waitHostBack(server, orch, deadline); err != nil {
		return fmt.Errorf("the VM %s/%s of %s may be stopped or mid-reboot (check it in Proxmox, then run the rolling upgrade again): %w",
			vm.Node, vm.VMID, server.URL, err)
	}
	return nil
}

// hostActionDeadline is the deadline of a host action on server: the rolling host step's
// shared deadline when one runs on it, else prov-db-host-reboot-timeout from now.
func (cluster *Cluster) hostActionDeadline(server *ServerMonitor) time.Time {
	if server != nil {
		if d, ok := cluster.hostStepDeadlines.Load(server.URL); ok {
			if deadline, ok := d.(time.Time); ok {
				return deadline
			}
		}
	}
	return time.Now().Add(cluster.hostRebootTimeout())
}

// waitHostBack waits for the hypervisor to report the server's VM running and for the guest
// to answer on its SSH port, until deadline (the host step's, shared with the reboot).
func (cluster *Cluster) waitHostBack(server *ServerMonitor, orch Orchestrator, deadline time.Time) error {
	address := net.JoinHostPort(misc.Unbracket(server.Host), strconv.Itoa(cluster.Conf.OnPremiseSSHPort))
	last := "no answer yet"
	for {
		st, err := orch.HostStatus(server)
		switch {
		case err != nil:
			last = err.Error()
		case st.State != "running":
			last = "the VM is " + st.State
		default:
			if err := hostGuestReachable(address, 5*time.Second); err != nil {
				last = "the guest does not answer on " + address + ": " + err.Error()
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
					"Rolling upgrade: the host of %s is back (node %s, vmid %s)", server.URL, st.Node, st.VMID)
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the host is not back within %s (prov-db-host-reboot-timeout, the reboot included): %s", cluster.hostRebootTimeout(), last)
		}
		time.Sleep(hostWaitInterval)
	}
}

// HostActionRefusal is why the API refuses to shut down or reboot the VM of server, nil when
// it may: the database on the VM must be stopped (the monitor sees it failed) or the server in
// maintenance, and a running master is always refused (switch over first). The rolling host
// step does not ask: it reboots a server whose database it stopped itself.
func (cluster *Cluster) HostActionRefusal(server *ServerMonitor) error {
	if server == nil {
		return errors.New("no server")
	}
	if server.State == stateFailed {
		return nil
	}
	if server.IsMaster() {
		return fmt.Errorf("%s is the running master: switch over, or stop its database, before its VM goes down", server.URL)
	}
	if !server.IsMaintenance {
		return fmt.Errorf("the database on %s is running and the server is not in maintenance: stop the database or put the server in maintenance before its VM goes down", server.URL)
	}
	return nil
}

// CheckRollingUpgradeHost reports, at each tick, the configuration facts that leave the host
// step of the rolling upgrade without effect while prov-db-rolling-upgrade-host is on: a
// backend that does not drive the hosts (WARN0350), servers without a VM (WARN0351). Config
// domain, WARNING (T3): the rolling upgrade still runs, without the reboot. No I/O.
func (cluster *Cluster) CheckRollingUpgradeHost() {
	if cluster.ConfigStateMachine == nil || cluster.Conf == nil || !cluster.Conf.ProvDbRollingUpgradeHost {
		return
	}
	orch := cluster.Orchestrator()
	if !orch.Traits().HasHost {
		cluster.ConfigStateMachine.AddState("WARN0350", state.State{ErrType: "WARNING", ErrKey: "WARN0350", ErrFrom: "CONF",
			ErrDesc: fmt.Sprintf(clusterError["WARN0350"], orch.Name(), orch.Type())})
		return
	}
	var missing []string
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		if _, ok, err := cluster.hostVM(server); err != nil || !ok {
			missing = append(missing, server.URL)
		}
	}
	if len(missing) > 0 {
		cluster.ConfigStateMachine.AddState("WARN0351", state.State{ErrType: "WARNING", ErrKey: "WARN0351", ErrFrom: "CONF",
			ErrDesc: fmt.Sprintf(clusterError["WARN0351"], strings.Join(missing, ", "))})
	}
}
