# Proxmox VE orchestrator

For database servers that run as virtual machines on Proxmox VE. The `proxmox`
orchestrator is the on-premise orchestrator (replication-manager reaches each
database host over SSH, exactly as with `onpremise`) plus the machine under
each server: replication-manager can shut down, start, reboot and read the
status of the VM of a database server through the Proxmox VE API, and the
rolling upgrade can reboot each VM, one server after the other, between the
stop of its database and its upgrade.

Everything the on-premise orchestrator does (provision through the SSH
scripts, start, stop, the rolling restart and upgrade, proxies, the jobs) is
unchanged on a `proxmox` cluster.

## Turning it on

The orchestrator is opt-in: it must be listed in `prov-orchestrator-enable`
and chosen per cluster in `prov-orchestrator`. Turning it off is
`prov-orchestrator = "onpremise"`: the services keep running as they are.

```toml
[DEFAULT]
prov-orchestrator-enable = "onpremise,local,proxmox"

[cluster1]
prov-orchestrator = "proxmox"
onpremise-ssh = true                       # the guest access, as on-premise
proxmox-api-url = "https://pve.example:8006"
proxmox-api-token = "repman@pve!rolling=<secret>"
prov-db-host-vmids = "db1=pve1/101,db2=pve1/102,db3=pve2/103"
prov-db-rolling-upgrade-host = true
prov-db-host-upgrade-script = "/etc/replication-manager/host-upgrade.sh"
prov-db-host-reboot-timeout = 900
```

## Settings

| Setting | Default | |
|---|---|---|
| `proxmox-api-url` | | the Proxmox VE API, `https://host:8006` (`/api2/json` is added) |
| `proxmox-api-token` | | an API token, `user@realm!tokenid=secret`. A secret: encrypted in the saved configuration and in the credential stores like the other credentials, never written to a log |
| `proxmox-api-insecure` | `false` | accept the API's certificate without verification: a lab with the self-signed certificate of a fresh install only |
| `prov-db-host-vmids` | | the VM under each database server: `host=node/vmid` pairs, comma separated. `host` is the server's host, `host:port`, or name, as in `db-servers-hosts` |
| `prov-db-rolling-upgrade-host` | `false` | the host step of the rolling upgrade (below) |
| `prov-db-host-upgrade-script` | | a local script run over SSH in the guest before its reboot (the kernel and package upgrade); empty: a reboot only |
| `prov-db-host-reboot-timeout` | `600` | seconds a host action may take; in the rolling upgrade, one bound for the reboot and the return of the VM together (running again and answering on its SSH port) |

All of them are per cluster, set in the configuration file or in the GUI
(cluster, Configs, Databases, Orchestrator VM, shown when the cluster's
orchestrator is `proxmox`) or the settings API. A value set in a file under
`/etc` cannot be changed by the API.

### The API token

Create a token for a dedicated user in the Proxmox VE GUI (Datacenter,
Permissions, API Tokens) and give it the `VM.PowerMgmt` and `VM.Audit`
privileges on the VMs of the database servers (a role with these two
privileges on `/vms/<vmid>` or a pool holding them). Nothing else is used:
replication-manager never creates, changes or deletes a VM.

## The VM of a server

In the GUI, on a `proxmox` cluster, the menu of each database server
(Provision) has:

- **Host (VM) Status**: the node, the vmid, the state (`running`, `stopped`,
  `paused`) and the uptime of the VM, and the host action running on it;
- **Reboot Host (VM)**: a clean reboot (the guest shuts down, then starts); a
  stopped VM is started;
- **Shutdown Host (VM)**: a clean shutdown (ACPI or the guest agent). The hard
  stop of the API is never used;
- **Start Host (VM)**.

The database inside the VM goes down with it, so a shutdown or a reboot is
refused (`409`, with the reason) while the database on the VM runs, unless
the server is in maintenance, and always on the running master: stop the
database or switch over first, or use the rolling upgrade, which stops each
database itself. A start is never refused. One host action runs at a time per
server.

API (each needs the grants named, like the database start and stop):

| Method and path | Grant |
|---|---|
| `POST /api/clusters/{cluster}/servers/{server}/actions/host-stop` | `db-stop` |
| `POST /api/clusters/{cluster}/servers/{server}/actions/host-start` | `db-start` |
| `POST /api/clusters/{cluster}/servers/{server}/actions/host-reboot` | `db-start` and `db-stop` |
| `GET /api/clusters/{cluster}/servers/{server}/host-status` | `db-show-status` |

The actions answer `202` and run in the background; their result is in the
orchestrator log. Another orchestrator answers `501`. Each action is logged
with the `pvesh` command that reproduces it, for example
`pvesh create /nodes/pve1/qemu/101/status/reboot`.

## The rolling upgrade with the host step

With `prov-db-rolling-upgrade-host = true`, the rolling upgrade (GUI, API,
CLI) runs for each server, the replicas first, then the old master after a
switchover:

1. maintenance on, wait for a running binlog backup;
2. the clean stop of the database (as on-premise);
3. **the host step**: `prov-db-host-upgrade-script` in the guest over SSH,
   then the reboot of the VM through the API, then a wait for the VM to be
   `running` **on a new boot** (its uptime restarted: an old guest still up
   before the reboot begins is not taken for a rebooted one) and its SSH
   port to answer; the reboot and the wait share one
   `prov-db-host-reboot-timeout`;
4. `onpremise-ssh-upgrade-db-script` (the database packages and the upgrade
   tool, as on-premise), the wait for the database and its replication;
5. maintenance off.

A failed upgrade script, a refused reboot or a VM that is not back in time
stops the rolling upgrade at that server, as any failed step; the error names
the VM (`node/vmid`) and says it may be stopped or mid-reboot: check it in
Proxmox, then run the rolling upgrade again. A server
without a VM in `prov-db-host-vmids` is upgraded without the host step.

Two configuration warnings tell when the host step is on but has no effect:
`WARN0350` (the cluster's orchestrator does not drive the hosts) and
`WARN0351` (servers without a VM in `prov-db-host-vmids`, named).

The VM counts as back only once Proxmox reports a new boot (its uptime
restarted). On a setup where the uptime would not restart after an API
reboot, the step fails at the timeout with "has not rebooted yet": the
database is never upgraded on a guest that did not reboot. A status refused
by Proxmox (the token, a privilege, no such VM) stops the step at once.
