# Enterprise Plugins

## Overview

Four built-in plugins provide enterprise features compiled into the repman binary and registered via `init()` — no external binary distribution needed.

### Advisory Plugins

| Plugin | Error key range | Scope |
|---|---|---|
| **enterprise-security** | `ENT0001`–`ENT9999`, `CVE-*` | All MariaDB/MySQL CVEs |
| **enterprise-replication** | `RPL0001`–`RPL9999`, `CVE-*-replication-*` | Replication subsystem bugs |
| **enterprise-workload** | `WRK0001`–`WRK9999`, `CVE-*-workload-*` | CRITICAL/HIGH crash, deadlock, memory leak |

All advisory findings route to `SecurityStateMachine` → **Security Logs tab** (not global warnings).

### Compliance Refresh Plugin

| Plugin | Error keys | Scope |
|---|---|---|
| **enterprise-compliance** | `ENTCOMP001`, `ENTCOMPERR001` | Compliance moduleset change detection |

The compliance plugin monitors for updated best-practice compliance modulesets. When `prov-auto-update-compliance=true` (default), updates are applied automatically — preserved variables are never overwritten. When `false`, changes require explicit user approval via the API or GUI.

---

## Version matching

Each advisory entry has `affected_from` and `fixed_in` version strings. The plugin checks:

1. **Database flavors** (`MariaDB`, `MySQL`, `Percona`) — compared against `ServerVersion` (per-server)
2. **Tool flavors** (`repman`, `proxysql`, `maxscale`, `haproxy`, `restic`, …) — looked up in `ClusterContext.ToolVersions`

If the server/tool version is **below** `affected_from` → not yet affected, skip.
If the server/tool version is **at or above** `fixed_in` → already fixed, skip.
Otherwise → emit finding.

Findings **auto-resolve** when the server or tool is upgraded past `fixed_in`.

### ToolVersions map

`ClusterContext.ToolVersions` is populated by `buildClusterContext()` in `srv_log_plugins.go`:

| Key | Source |
|---|---|
| `repman` | `cluster.Conf.FullVersion` (linker-set build version) |
| `mariadb` / `mysql` | `server.DBVersion` (per-server, from live connection) |
| `proxysql` / `maxscale` / `haproxy` | `proxy.Version` (from proxy monitoring) |
| `client` / `client-dump` / `client-binlog` | `cluster.VersionsMap` (detected binary versions) |
| `mydumper` / `restic` / `sysbench` | `cluster.VersionsMap` |

---

## Advisory JSON format

All three advisory plugins use the same JSON schema:

```json
{
  "version": "1",
  "generated_at": "2026-04-25T09:10:15Z",
  "source": "signal18-backoffice",
  "issues": [
    {
      "id": "ENT0001",
      "cve": "CVE-2022-27458",
      "mariadb_jira": "MDEV-26281",
      "github_issue": "",
      "severity": "SECURITY",
      "title": "MariaDB use-after-free in Binary_string::free_buffer()",
      "description": "Server {server_url} is running {flavor} {version} which is affected by ...",
      "flavor": "MariaDB",
      "affected_from": "10.7.0",
      "fixed_in": "10.7.3",
      "remediations": [
        {
          "type": "repman_config",
          "description": "Upgrade to MariaDB 10.7.3 or later.",
          "risk": "disruptive"
        }
      ]
    }
  ]
}
```

### Fields

| Field | Description |
|---|---|
| `id` | Unique key → `Finding.ErrKey` (e.g. `ENT0001`, `RPL0003`, `CVE-2023-5157-mariadb-10.6`) |
| `cve` | CVE identifier (optional, for display) |
| `mariadb_jira` | MDEV ticket (optional) |
| `github_issue` | `owner/repo#NNN` (optional) |
| `severity` | `SECURITY` / `WARNING` / `ERROR` (overridden to `SECURITY` by built-in plugins) |
| `title` | Short title |
| `description` | Supports `{server_url}`, `{flavor}`, `{version}` placeholders |
| `flavor` | `MariaDB` / `MySQL` / `Percona` / `repman` / `proxysql` / `""` (empty = all) |
| `affected_from` | `"major.minor.release"` or `""` (all versions from the start) |
| `fixed_in` | `"major.minor.release"` or `""` (not yet fixed — always emits) |
| `remediations` | Array of `{type, description, sql, my_cnf, config_key, config_value, risk}` |

### File resolution (first found wins)

1. `{ShareDir}/plugins/data/enterprise-{security,replication,workload}-issues.json` — back-office deployed via git pull
2. Embedded default compiled into the binary via `go:embed`

---

## Subscription plan gating

### Plugin side

Each enterprise plugin reads `cloud18-subscription-plan` from the plugin Config map (injected by `resolvePluginConfig()`). When the plan is empty or `"free"`, a persistent security error is emitted:

| Error key | Plugin | Message |
|---|---|---|
| `ENTERR001` | enterprise-security | "enterprise security advisories are not refreshed on the free plan…" |
| `RPLERR001` | enterprise-replication | "enterprise replication advisories are not refreshed on the free plan…" |
| `WRKERR001` | enterprise-workload | "enterprise workload advisories are not refreshed on the free plan…" |
| `ENTCOMPERR001` | enterprise-compliance | "compliance modulesets are not refreshed on the free plan…" |

---

## Data flow

`syncPluginDataFromPull()` in `server/server_git.go` copies files from `pullDir/plugins/data/` → `ShareDir/plugins/data/` on every git pull (MD5 dedup — unchanged files are skipped).

The built-in advisory plugins read from `src.PluginDataDir` which is `ShareDir/plugins/data/`.

---

## Compliance refresh

### Change detection and approval

```
New compliance arrives (BO push or binary upgrade)
  → CheckComplianceUpdate() compares CRC32 vs last accepted
  → WARN0168 raised on cluster state machine
  → Auto-accepted if prov-auto-update-compliance=true (default)
  → Or waits for user approval via API/GUI
  → AcceptComplianceUpdate() loads new modules + saves to disk
  → WARN0168 cleared
```

### Persisted accepted compliance

On acceptance, the full compliance modules are saved to disk in the cluster working directory. The previous version is rotated to `.old` for diffing:

| File | Content |
|---|---|
| `accepted_compliance_db.json` | Current accepted DB compliance module |
| `accepted_compliance_proxy.json` | Current accepted Proxy compliance module |
| `accepted_compliance_db.json.old` | Previous version (for diff) |
| `accepted_compliance_proxy.json.old` | Previous version (for diff) |

On startup, the accepted version is loaded from disk instead of the embedded module, preserving the accepted state across binary upgrades.

### Configuration

#### `prov-auto-update-compliance`

| Field | Value |
|---|---|
| Type | `bool` |
| Default | `true` |
| Config key | `prov-auto-update-compliance` |
| GUI | Database Configurator → Auto-Update Compliance toggle |

When `true` (default): compliance updates are auto-accepted. WARN0168 fires and auto-clears. Preserved variables are never overwritten.

When `false`: WARN0168 stays open. The operator reviews changes in the Configurator (Review Changes button) and accepts when ready.

### Cluster state: WARN0168

| Field | Value |
|---|---|
| Error key | `WARN0168` |
| Type | `WARNING` |
| From | `CLUSTER` |
| Check frequency | Every 3600 monitoring ticks (~2 hours at 2s tick) |
| Auto-clear | Yes, when `prov-auto-update-compliance=true` |

### API

#### `POST /api/clusters/{clusterName}/settings/actions/accept-compliance`

Accepts the pending compliance update, reloads the configurator, saves to disk, clears WARN0168.

**Required grants** (OR logic): `db-config-accept-compliance`, `proxy-config-accept-compliance`

#### `GET /api/clusters/{clusterName}/configurator/compliance-diff`

Returns a structured diff between the previous and current compliance showing per-tag changes (added, removed, modified with old/new cnf content).

### Grants

| Grant | Shorthand | Purpose |
|---|---|---|
| `db-config-accept-compliance` | Part of `db` | Accept DB compliance update |
| `proxy-config-accept-compliance` | Part of `proxy` | Accept Proxy compliance update |

---

## GUI

- **Settings → Plugins**: enable/disable toggle for all four enterprise plugins
- **Database Configurator → Auto-Update Compliance**: toggle for `prov-auto-update-compliance`
- **Database Configurator**: warning banner with Review Changes / Accept buttons when WARN0168 is active
- **ComplianceDiffModal**: side-by-side diff of changed tags (added/removed/modified)
- **Tag content viewer**: eye icon on each tag shows cnf content + documentation links
- **Security button** (navbar): count of open security alerts
- **Security Logs tab**: enterprise advisory and compliance findings

---

## Code locations

### Advisory plugins

| File | Responsibility |
|---|---|
| `cluster/logplugin/plugin_enterprise_security.go` | Built-in security plugin + shared helpers |
| `cluster/logplugin/plugin_enterprise_replication.go` | Built-in replication plugin |
| `cluster/logplugin/plugin_enterprise_workload.go` | Built-in workload plugin |
| `cluster/logplugin/logplugin.go` | `ClusterContext.ToolVersions` definition |
| `cluster/logplugin/plugins/wire/wire.go` | `ClusterContext.ToolVersions` in wire protocol |
| `cluster/srv_log_plugins.go` | `buildClusterContext()`, `resolvePluginConfig()` |
| `server/server_git.go` | `syncPluginDataFromPull()`, DocHelp auto-reload |

### Compliance refresh

| File | Responsibility |
|---|---|
| `cluster/logplugin/plugin_enterprise_compliance.go` | Compliance change detection (ENTCOMP001, ENTCOMPERR001) |
| `cluster/configurator/configurator.go` | `CheckComplianceUpdate()`, `AcceptComplianceUpdate()`, `ComplianceDiff()`, CRC persistence |
| `cluster/cluster_version.go` | Cluster-level check/accept, WARN0168 management |
| `cluster/cluster_acl_rules.go` | ACL rule for accept-compliance |
| `config/grants.go` | `GrantDBConfigAcceptCompliance`, `GrantProxyConfigAcceptCompliance` |
| `config/error.go` | WARN0168 message format |
| `server/api_cluster.go` | `handlerMuxAcceptCompliance`, `handlerMuxComplianceDiff` |

### Configurator helpers

| File | Responsibility |
|---|---|
| `cluster/configurator/configurator.go` | `GetTagMyCnf()`, `ParseVariableNamesFromCnf()`, `NormaliseVariableName()` |
| `cluster/configurator/dochelp.go` | DocHelp loader (atomic pointer, embedded + disk override) |
| `share/plugins/data/enterprise-dochelp-variables.json` | 755 variable→doc URL mappings |
| `server/api_cluster.go` | `handlerMuxGetTagContent` (tag content + doc help) |

### GUI components

| File | Responsibility |
|---|---|
| `share/dashboard_react/src/Pages/Settings/PluginsSettings.jsx` | Plugin toggles + descriptions |
| `share/dashboard_react/src/Pages/Configs/components/DBConfigs.jsx` | Auto-Update toggle, compliance banner, tag viewer |
| `share/dashboard_react/src/components/Modals/TagContentModal/` | Config content + doc links modal |
| `share/dashboard_react/src/components/Modals/ComplianceDiffModal/` | Compliance diff review modal |
| `share/dashboard_react/src/components/AddRemovePill/` | Tag pill with eye icon |
| `share/dashboard_react/src/components/Navbar/index.jsx` | Security badge (alert count) |
