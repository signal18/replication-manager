# Enterprise Plugins

## Overview

Four built-in plugins provide enterprise features compiled into the repman binary and registered via `init()` — no external binary distribution needed.

### Advisory Plugins

| Plugin | Error key range | Scope | NVD query |
|---|---|---|---|
| **enterprise-security** | `ENT0001`–`ENT9999`, `CVE-*` | All MariaDB/MySQL CVEs | `mariadb` / `mysql+oracle` |
| **enterprise-replication** | `RPL0001`–`RPL9999`, `CVE-*-replication-*` | Replication subsystem bugs | `mariadb+replication` / `mysql+replication+oracle` |
| **enterprise-workload** | `WRK0001`–`WRK9999`, `CVE-*-workload-*` | CRITICAL/HIGH crash, deadlock, memory leak | `cvssV3Severity=CRITICAL` + `HIGH`, dedup vs other two |

All advisory findings route to `SecurityStateMachine` → **Security Logs tab** (not global warnings).

### Compliance Refresh Plugin

| Plugin | Error keys | Scope |
|---|---|---|
| **enterprise-compliance** | `ENTCOMP001`, `ENTCOMPERR001` | Compliance moduleset change detection |

The compliance plugin monitors for updated best-practice compliance modulesets pushed by the back office or shipped with a binary upgrade. When `prov-auto-update-compliance=true` (default), updates are applied automatically — preserved variables are never overwritten. When `false`, changes require explicit user approval via the API or GUI.

---

## How it works

```
Back Office (dbaas-portal)          repman instance
┌──────────────────────────┐        ┌───────────────────────────────────┐
│ portal_cron.sh            │        │                                   │
│  ├─ generate-enterprise-  │  git   │  git pull → syncPluginDataFromPull│
│  │  security-issues.sh   │──push──▶│  → ShareDir/plugins/data/         │
│  ├─ generate-enterprise-  │        │    enterprise-*-issues.json       │
│  │  replication-issues.sh │        │                                   │
│  └─ generate-enterprise-  │        │  Built-in plugin reads JSON       │
│     workload-issues.sh   │        │  → version match → findings       │
└──────────────────────────┘        │  → SecurityStateMachine           │
                                    └───────────────────────────────────┘
```

### Version matching

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

All three plugins use the same JSON schema:

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

### Back-office side

The BO generation scripts query the `dbaas` database for instances with a paid plan:

```sql
SELECT DISTINCT cc.domain, cc.subdomain, cc.value AS plan
FROM clusters_config cc
WHERE cc.variable = 'cloud18-subscription-plan'
  AND cc.value IN ('support', 'support-services', 'partner')
```

Only those instances' pull repos receive the updated JSON. Free-plan instances never get new files — they run on the stale embedded default.

### Plugin side

Each enterprise plugin reads `cloud18-subscription-plan` from the plugin Config map (injected by `resolvePluginConfig()`). When the plan is empty or `"free"`, a persistent security error is emitted:

| Error key | Plugin | Message |
|---|---|---|
| `ENTERR001` | enterprise-security | "enterprise security advisories are not refreshed on the free plan…" |
| `RPLERR001` | enterprise-replication | "enterprise replication advisories are not refreshed on the free plan…" |
| `WRKERR001` | enterprise-workload | "enterprise workload advisories are not refreshed on the free plan…" |

---

## Back-office scripts

Located in `scripts/backoffice/` (repman repo) and `scripts/` (dbaas-portal repo).

### generate-enterprise-security-issues.sh

Sources: hand-curated `cve-entries.csv` + NVD (`mariadb`, `mysql+oracle`) + GitHub issues labelled `security`.

### generate-enterprise-replication-issues.sh

Sources: hand-curated `replication-entries.csv` (MDEV-20821, MDEV-28310, MDEV-19577 per branch) + NVD (`mariadb+replication`, `mysql+replication+oracle`).

### generate-enterprise-workload-issues.sh

Sources: hand-curated `workload-entries.csv` (MDEV-31404, MDEV-29644, MDEV-30820, MDEV-29032, MDEV-31105 per branch) + NVD CRITICAL + HIGH severity, deduplicated against the other two advisory files.

### Common features

- **Daily guard**: stamp file `.enterprise-{type}-last-run` — script only runs once per 24h. Use `--force` to bypass.
- **DB client**: `mariadb_client()` function — deferred eval, same connection as `inject_plugins.sh`.
- **Deploy**: copies JSON to `plugins/data/enterprise-{type}-issues.json` at the pull repo root, git commit + push. File is instance-wide (not per-cluster).
- **Wired into**: `portal_cron.sh` after `inject_plugins.sh`, before `pull_commit_push_all.sh`.

---

## Data flow: git pull → plugin

`syncPluginDataFromPull()` in `server/server_git.go` copies files from `pullDir/plugins/data/` → `ShareDir/plugins/data/` on every git pull (MD5 dedup — unchanged files are skipped).

The built-in advisory plugins read from `src.PluginDataDir` which is `ShareDir/plugins/data/`.

---

## Enterprise Compliance Refresh

### Overview

The back office downloads compliance modulesets from the OpenSVC collector (`collector.signal18.io`) and pushes them to paid instances' pull repos. The compliance refresh plugin detects the change and manages the approval workflow.

### Collector API

| Module | ID | URL |
|---|---|---|
| Database | 350 | `GET https://collector.signal18.io/init/rest/api/compliance/modulesets/350/export` |
| Proxy | 352 | `GET https://collector.signal18.io/init/rest/api/compliance/modulesets/352/export` |

Authentication: basic auth via OpenSVC secrets (`COLLECTOR_USER`, `COLLECTOR_PASSWORD`).

### Back-office script

`generate-compliance-refresh.sh` in `dbaas-portal/scripts/`:
- Downloads DB + Proxy modulesets from collector
- Strips the `modulesets` key (embedded format only has `filtersets` + `rulesets`)
- Pushes to `plugins/data/moduleset_mariadb.svc.mrm.db.json` and `moduleset_mariadb.svc.mrm.proxy.json` in each paid instance's pull repo
- **30-minute guard** (not daily — compliance changes should propagate faster than CVE advisories)
- Wired into `portal_cron.sh` after `generate-dochelp-variables.sh`

### Change detection and approval

The compliance is **not auto-reloaded** by default. Changes go through an approval workflow:

```
BO pushes new compliance → git pull → syncPluginDataFromPull
  → files land in ShareDir/plugins/data/
  → CheckComplianceUpdate() compares CRC32 vs last accepted
  → WARN0168 raised on cluster state machine
  → User approves via API (or auto-accepted if prov-auto-update-compliance=true)
  → AcceptComplianceUpdate() loads new modules + saves to disk
  → WARN0168 cleared
```

The same flow applies to **binary upgrades**: on restart, the previously accepted compliance is loaded from disk. If the new embedded module has a different CRC, `WARN0168` fires.

### Persisted accepted compliance

On acceptance, the full compliance modules are saved to disk in the cluster working directory:

| File | Content |
|---|---|
| `{WorkingDir}/{cluster}/accepted_compliance_db.json` | Last accepted DB compliance module |
| `{WorkingDir}/{cluster}/accepted_compliance_proxy.json` | Last accepted Proxy compliance module |

On startup, these are loaded **instead of the embedded modules** to preserve the accepted version across binary upgrades. First run (no files on disk) uses the embedded modules as baseline.

### Configuration

#### `prov-auto-update-compliance`

| Field | Value |
|---|---|
| Type | `bool` |
| Default | `true` |
| Config key | `prov-auto-update-compliance` |

When `true` (default): compliance changes are auto-accepted immediately. WARN0168 fires and auto-clears in the same tick. This preserves backward compatibility — upgrades apply compliance changes silently as before.

When `false`: WARN0168 stays open until the user explicitly approves via the API. The old accepted compliance remains active for config generation until approved.

### Cluster state

#### WARN0168 — New compliance available

| Field | Value |
|---|---|
| Error key | `WARN0168` |
| Type | `WARNING` |
| From | `CLUSTER` |
| Message | `New compliance moduleset available from back office (DB CRC: XXXXXXXX → YYYYYYYY, Proxy CRC: XXXXXXXX → YYYYYYYY). Accept the update via Settings to apply.` |
| Check frequency | Every 3600 monitoring ticks (~2 hours at 2s tick) |
| Auto-clear | Yes, when `prov-auto-update-compliance=true` |

### Security log findings

| Error key | Severity | Plugin | Condition |
|---|---|---|---|
| `ENTCOMP001` | SECURITY | enterprise-compliance | Compliance moduleset refreshed (CRC changed) |
| `ENTCOMPERR001` | SECURITY | enterprise-compliance | Free plan — no compliance files pushed by BO |

### API endpoint

#### `POST /api/clusters/{clusterName}/settings/actions/accept-compliance`

Accepts the pending compliance update, reloads the configurator, saves to disk, clears WARN0168.

**Required grants** (OR logic):
- `db-config-accept-compliance`
- `proxy-config-accept-compliance`

**Response:**
- `200` — `{"status":"compliance update accepted"}`
- `404` — cluster not found
- `403` — no valid ACL
- `409` — no pending compliance update

### Grants

| Grant | Shorthand | Purpose |
|---|---|---|
| `db-config-accept-compliance` | Part of `db` | Accept DB compliance update |
| `proxy-config-accept-compliance` | Part of `proxy` | Accept Proxy compliance update |

Users with the `db` or `proxy` ACL shorthand (e.g. `admin:cluster db proxy ...`) automatically get these grants.

---

## GUI

The three plugins appear in **Settings → Plugins** with:

- Enable/disable toggle (same as all plugins)
- Help button with markdown description listing advisory sources and tracked MDEV issues

No user-configurable parameters — the advisory JSON is managed by the back office.

The **Security button** in the navbar shows the count of open security alerts (from `SecurityStateMachine`). The score letter grade remains inside the Security Score modal.

---

## Code locations

### Advisory plugins

| File | Responsibility |
|---|---|
| `cluster/logplugin/plugin_enterprise_security.go` | Built-in security plugin + shared helpers (`entMatchIssue`, `entParseVersion`, `entVersionLess`) |
| `cluster/logplugin/plugin_enterprise_replication.go` | Built-in replication plugin |
| `cluster/logplugin/plugin_enterprise_workload.go` | Built-in workload plugin |
| `cluster/logplugin/plugins/plugin-enterprise-security/` | External binary + embedded JSON |
| `cluster/logplugin/plugins/plugin-enterprise-replication/` | External binary + embedded JSON |
| `cluster/logplugin/plugins/plugin-enterprise-workload/` | External binary + embedded JSON |
| `cluster/logplugin/logplugin.go` | `ClusterContext.ToolVersions` definition |
| `cluster/logplugin/plugins/wire/wire.go` | `ClusterContext.ToolVersions` in wire protocol |
| `cluster/srv_log_plugins.go` | `buildClusterContext()` (ToolVersions population), `resolvePluginConfig()` (plan injection) |
| `server/server_git.go` | `syncPluginDataFromPull()`, DocHelp auto-reload |
| `share/dashboard_react/src/Pages/Settings/PluginsSettings.jsx` | GUI toggle + descriptions |
| `share/dashboard_react/src/components/Navbar/index.jsx` | Security badge (alert count) |

### Compliance refresh

| File | Responsibility |
|---|---|
| `cluster/logplugin/plugin_enterprise_compliance.go` | Built-in compliance change detection plugin (ENTCOMP001, ENTCOMPERR001) |
| `cluster/configurator/configurator.go` | `CheckComplianceUpdate()`, `AcceptComplianceUpdate()`, `ReloadComplianceFromDataDir()`, `saveAcceptedCompliance()`, `loadAcceptedCompliance()`, `complianceCRC()` |
| `cluster/cluster_version.go` | `CheckComplianceUpdate()` (cluster-level), `AcceptComplianceUpdate()` (cluster-level), WARN0168 management |
| `cluster/cluster_acl_rules.go` | ACL rule for `/settings/actions/accept-compliance` |
| `config/grants.go` | `GrantDBConfigAcceptCompliance`, `GrantProxyConfigAcceptCompliance` |
| `config/error.go` | WARN0168 message format |
| `server/api_cluster.go` | `handlerMuxAcceptCompliance` API handler, route registration |
| `server/server_git.go` | `syncPluginDataFromPull()` — copies compliance files from pull repo |

### Back-office scripts (dbaas-portal repo)

| File | Responsibility |
|---|---|
| `scripts/generate-enterprise-security-issues.sh` | NVD + GitHub CVE advisory generator |
| `scripts/generate-enterprise-replication-issues.sh` | NVD + MDEV replication advisory generator |
| `scripts/generate-enterprise-workload-issues.sh` | NVD CRITICAL/HIGH workload advisory generator |
| `scripts/generate-compliance-refresh.sh` | OpenSVC collector compliance downloader (30-min guard) |
| `scripts/generate-dochelp-variables.sh` | MariaDB KB + MySQL doc URL scraper |
| `scripts/cve-entries.csv` | Hand-curated CVE entries |
| `scripts/replication-entries.csv` | Hand-curated MDEV replication bugs |
| `scripts/workload-entries.csv` | Hand-curated MDEV crash/performance bugs |
| `scripts/blog-references.csv` | Community blog references (154 entries, 20 sources) |
| `scripts/portal_cron.sh` | Orchestrates all BO scripts |
