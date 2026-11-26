// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/proxmox"
)

// proxmoxOrchestrator is the Proxmox VE backend of the Orchestrator interface (design
// section 10.4): the on-premise backend's service operations (SSH into the guest, the
// embedded onPremiseOrchestrator, unchanged) plus the HostOrchestrator facet, the VM under
// each database server driven through the Proxmox REST API. The VM of a server is named by
// prov-db-host-vmids (host=node/vmid); the API by proxmox-api-url and proxmox-api-token (a
// secret, never logged: F10).
type proxmoxOrchestrator struct {
	*onPremiseOrchestrator
}

var _ Orchestrator = (*proxmoxOrchestrator)(nil)

// defaultHostRebootTimeout bounds a host action when prov-db-host-reboot-timeout is unset.
const defaultHostRebootTimeout = 600 * time.Second

func newProxmoxOrchestrator(cluster *Cluster, name string) *proxmoxOrchestrator {
	o := &proxmoxOrchestrator{onPremiseOrchestrator: newOnPremiseOrchestrator(cluster, name)}
	// the not-supported answers name this backend, not the embedded one
	o.onPremiseOrchestrator.unsupportedOrchestrator.orch = o
	return o
}

func (o *proxmoxOrchestrator) Name() string { return o.name }
func (o *proxmoxOrchestrator) Type() string { return config.ConstOrchestratorProxmox }

// Traits are the on-premise ones plus the host.
func (o *proxmoxOrchestrator) Traits() OrchestratorTraits {
	t := o.onPremiseOrchestrator.Traits()
	t.HasHost = true
	return t
}

// Check tells whether the Proxmox API answers and accepts the token.
func (o *proxmoxOrchestrator) Check() error {
	client, err := o.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxmox.DefaultTimeout)
	defer cancel()
	_, err = client.Version(ctx)
	return err
}

// proxmoxVM is the VM under a database server.
type proxmoxVM struct {
	Node string
	VMID string
}

// parseHostVMIDs reads prov-db-host-vmids: host=node/vmid pairs, comma separated. A host is
// a database server's host, host:port, or name.
func parseHostVMIDs(value string) (map[string]proxmoxVM, error) {
	out := map[string]proxmoxVM{}
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		host, target, ok := strings.Cut(pair, "=")
		node, vmid, ok2 := strings.Cut(strings.TrimSpace(target), "/")
		host, node, vmid = strings.TrimSpace(host), strings.TrimSpace(node), strings.TrimSpace(vmid)
		if !ok || !ok2 || host == "" || node == "" || vmid == "" {
			return nil, fmt.Errorf("prov-db-host-vmids: %q is not host=node/vmid", pair)
		}
		out[host] = proxmoxVM{Node: node, VMID: vmid}
	}
	return out, nil
}

// hostVM is the VM of a server in prov-db-host-vmids, by host:port, host, then name.
func (cluster *Cluster) hostVM(server *ServerMonitor) (proxmoxVM, bool, error) {
	if server == nil {
		return proxmoxVM{}, false, errors.New("no server")
	}
	vms, err := parseHostVMIDs(cluster.Conf.ProvDbHostVmids)
	if err != nil {
		return proxmoxVM{}, false, err
	}
	host := misc.Unbracket(server.Host)
	for _, key := range []string{server.URL, host + ":" + server.Port, server.Host, host, server.Name} {
		if key == "" {
			continue
		}
		if vm, ok := vms[key]; ok {
			return vm, true, nil
		}
	}
	return proxmoxVM{}, false, nil
}

// hostRebootTimeout is prov-db-host-reboot-timeout, the bound of one host action.
func (cluster *Cluster) hostRebootTimeout() time.Duration {
	if cluster.Conf == nil || cluster.Conf.ProvDbHostRebootTimeout <= 0 {
		return defaultHostRebootTimeout
	}
	return time.Duration(cluster.Conf.ProvDbHostRebootTimeout) * time.Second
}

// client is the API client of the cluster's settings; the token is read from the secrets.
func (o *proxmoxOrchestrator) client() (*proxmox.Client, error) {
	conf := o.cluster.Conf
	token := ""
	if conf.Secrets != nil {
		token = conf.GetDecryptedValue("proxmox-api-token")
	}
	return proxmox.NewClient(conf.ProxmoxAPIURL, token, conf.ProxmoxAPIInsecure, proxmox.DefaultTimeout)
}

// vm resolves the server's VM, or the error that names what is missing.
func (o *proxmoxOrchestrator) vm(server *ServerMonitor) (proxmoxVM, error) {
	vm, ok, err := o.cluster.hostVM(server)
	if err != nil {
		return vm, err
	}
	if !ok {
		return vm, fmt.Errorf("server %s has no VM in prov-db-host-vmids", server.URL)
	}
	return vm, nil
}

// hostAction runs one VM action and waits for its task, until hostActionDeadline (the rolling
// host step's shared deadline, else prov-db-host-reboot-timeout from now).
// The line logged is the action and the pvesh command that reproduces it (F11), never the
// token (F10).
func (o *proxmoxOrchestrator) hostAction(server *ServerMonitor, action string, run func(context.Context, *proxmox.Client, proxmoxVM) (string, error)) error {
	vm, err := o.vm(server)
	if err != nil {
		return err
	}
	client, err := o.client()
	if err != nil {
		return err
	}
	o.cluster.LogModulePrintf(o.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Proxmox: %s of the VM of %s (node %s, vmid %s): pvesh create /nodes/%s/qemu/%s/status/%s",
		action, server.URL, vm.Node, vm.VMID, vm.Node, vm.VMID, action)
	ctx, cancel := context.WithDeadline(context.Background(), o.cluster.hostActionDeadline(server))
	defer cancel()
	upid, err := run(ctx, client, vm)
	if err != nil {
		return fmt.Errorf("proxmox %s of %s/%s: %w", action, vm.Node, vm.VMID, err)
	}
	if err := client.WaitTask(ctx, vm.Node, upid, 2*time.Second); err != nil {
		return fmt.Errorf("proxmox %s of %s/%s: %w", action, vm.Node, vm.VMID, err)
	}
	o.cluster.LogModulePrintf(o.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Proxmox: %s of the VM of %s (node %s, vmid %s) done", action, server.URL, vm.Node, vm.VMID)
	return nil
}

// StopHost shuts the VM down cleanly (the API's shutdown, never its hard stop).
func (o *proxmoxOrchestrator) StopHost(server *ServerMonitor) error {
	return o.hostAction(server, "shutdown", func(ctx context.Context, c *proxmox.Client, vm proxmoxVM) (string, error) {
		return c.ShutdownVM(ctx, vm.Node, vm.VMID)
	})
}

func (o *proxmoxOrchestrator) StartHost(server *ServerMonitor) error {
	return o.hostAction(server, "start", func(ctx context.Context, c *proxmox.Client, vm proxmoxVM) (string, error) {
		return c.StartVM(ctx, vm.Node, vm.VMID)
	})
}

// RebootHost reboots the VM; a VM found stopped is started (a reboot of a stopped VM is
// refused by the API, and the caller wants it running).
func (o *proxmoxOrchestrator) RebootHost(server *ServerMonitor) error {
	st, err := o.HostStatus(server)
	if err != nil {
		// the reboot is asked anyway: the API refuses it if the VM cannot be rebooted
		o.cluster.LogModulePrintf(o.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Proxmox: status of the VM of %s before its reboot failed, asking the reboot anyway: %s", server.URL, err)
	} else if st.State == "stopped" {
		return o.StartHost(server)
	}
	return o.hostAction(server, "reboot", func(ctx context.Context, c *proxmox.Client, vm proxmoxVM) (string, error) {
		return c.RebootVM(ctx, vm.Node, vm.VMID)
	})
}

func (o *proxmoxOrchestrator) HostStatus(server *ServerMonitor) (HostStatus, error) {
	vm, err := o.vm(server)
	if err != nil {
		return HostStatus{State: "unknown"}, err
	}
	client, err := o.client()
	if err != nil {
		return HostStatus{Node: vm.Node, VMID: vm.VMID, State: "unknown"}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxmox.DefaultTimeout)
	defer cancel()
	st, err := client.VMStatus(ctx, vm.Node, vm.VMID)
	if err != nil {
		return HostStatus{Node: vm.Node, VMID: vm.VMID, State: "unknown"}, err
	}
	state := st.Status
	if st.QMPStatus == "paused" {
		state = "paused"
	}
	if state == "" {
		state = "unknown"
	}
	return HostStatus{Node: vm.Node, VMID: vm.VMID, State: state, Uptime: time.Duration(st.Uptime) * time.Second}, nil
}
