# Security Logging

## Overview

replication-manager writes structured security events to two destinations in parallel:

| Destination | Field | Description |
|---|---|---|
| **HTTP ring buffer** | `cluster.LogSecurity` / `repman.Logs` | In-memory, shown in the dashboard Security Logs tab or Global Logs accordion |
| **Disk log file** | `cluster.SecurityLogrus` / `repman.SecurityLogrus` | Dedicated `*-security.log` file derived from `log-file`; structured JSON via logrus |

The disk log is activated automatically when `log-file` is configured. Its path is derived by inserting `-security` before the extension:

```
/var/log/replication-manager.log  →  /var/log/replication-manager-security.log
```

---

## Cluster-level events

These events are logged by the `cluster` package and written to the cluster's `LogSecurity` buffer (dashboard → cluster → Security Logs tab) and to `SecurityLogrus`.

### Grant assignment — `logUserGrantAssignment`

**Trigger:** Every time `cluster.LoadAPIUsers()` resolves the final grant set for a user (at startup, config reload, or ACL save).

**Log fields:**
```
user   = "<username>"
msg    = user "<username>" granted: [grant-a, grant-b, ...]
```

**Code:** `cluster/cluster_acl.go` → `logUserGrantAssignment()`

---

### User created — `AddUser`

**Trigger:** Successful call to `cluster.AddUser()` (via GUI Add User, API, or internal service account bootstrap such as the `system` user created on first `secret-login`).

**Log fields:**
```
user      = "<username>"
delegator = "<delegator>"
msg       = user "<username>" added to cluster "<cluster>" by delegator "<delegator>" with grants [<grants>]
```

**Code:** `cluster/cluster_add.go` → `AddUser()`

---

### User updated — `UpdateUser`

**Trigger:** Successful call to `cluster.UpdateUser()` (via GUI or API).

**Log fields:**
```
user      = "<username>"
delegator = "<delegator>"
msg       = user "<username>" updated in cluster "<cluster>" by delegator "<delegator>" with grants [<grants>]
```

**Code:** `cluster/cluster_add.go` → `UpdateUser()`

---

### User removed — `DropUser`

**Trigger:** Successful call to `cluster.DropUser()` (via GUI or API).

**Log fields:**
```
user = "<username>"
msg  = user "<username>" removed from cluster "<cluster>"
```

**Code:** `cluster/cluster_add.go` → `DropUser()`

---

## Server-level events

These events are logged by the `server` package via `repman.logSecurityEvent()`. They write to the main `repman.Logrus` logger (standard log file) and to `repman.SecurityLogrus` (`*-security.log`). They are **not** currently written to the HTTP ring buffer (no dashboard panel for server-level security events separate from the global log).

### API login failure — `api_auth_failure`

**Trigger:** Invalid credentials supplied to `POST /api/login`.

```json
{"event":"api_auth_failure","user":"alice","remote_addr":"10.0.0.1:54321","msg":"API authentication failure: invalid credentials"}
```

### API account locked — `api_account_locked`

**Trigger:** Third consecutive failed login within the 3-minute lockout window.

```json
{"event":"api_account_locked","user":"alice","remote_addr":"10.0.0.1:54321","msg":"API account locked for 3 min after repeated authentication failures"}
```

### API login success — `api_login_success`

**Trigger:** Successful `POST /api/login`, **only when** `monitoring-log-api-login = true` and the username is not in the silence list.

```json
{"event":"api_login_success","user":"alice","remote_addr":"10.0.0.1:54321","msg":"API login successful"}
```

### API secret login success — `api_secret_login_success`

**Trigger:** Successful `POST /api/clusters/{cluster}/servers/{server}/secret-login` (used by `dbjobs_new.sh`), **only when** `monitoring-log-api-login = true` and the username is not silenced.

```json
{"event":"api_secret_login_success","user":"system","remote_addr":"127.0.0.1:12345","msg":"API secret login successful (dbjobs/service account)"}
```

---

## Configuration

### `monitoring-log-api-login`

| Field | Value |
|---|---|
| Type | `bool` |
| Default | `false` |
| Scope | `server` (global, not per-cluster) |
| Config key | `monitoring-log-api-login` |
| JSON key | `monitoringLogApiLogin` |

When `true`, successful logins via `/api/login` and `/secret-login` are emitted as security events.  
Failure events (`api_auth_failure`, `api_account_locked`) are **always** logged regardless of this setting.

**GUI:** Global Settings accordion → **Log API Login** (toggle switch).

---

### `monitoring-log-api-login-silent-users`

| Field | Value |
|---|---|
| Type | `string` (comma-separated) |
| Default | `"system"` |
| Scope | `server` (global) |
| Config key | `monitoring-log-api-login-silent-users` |
| JSON key | `monitoringLogApiLoginSilentUsers` |

Usernames in this list are excluded from login success logging even when `monitoring-log-api-login = true`. Comparison is case-insensitive.

The default value `system` suppresses log noise from the `system` service account that `dbjobs_new.sh` creates on first secret-login and renews on every polling cycle.

**Example:**
```toml
monitoring-log-api-login = true
monitoring-log-api-login-silent-users = "system,healthcheck,monitor-bot"
```

**GUI:** Global Settings accordion → **Log API Login Silent Users** (text field).

---

## Grants

### `global-admin-show`

Grants read-only access to the global dashboard data endpoints:

| Endpoint | Description |
|---|---|
| `GET /api/global/http-logs` | Server-level log ring buffer (Global Logs accordion) |
| `GET /api/global/alerts` | Server-level errors and warnings |
| `GET /api/global/metrics` | Host CPU/memory/disk + repman process telemetry |

Without this grant the endpoints return `HTTP 403`. The `global` shorthand in `api-credentials-acl-allow` automatically includes `global-admin-show`.

---

### `global-admin-config`

Grants write access to global monitoring settings via:

| Endpoint | Description |
|---|---|
| `POST /api/clusters/settings/actions/switch/{setting}` | Toggle a global boolean setting |
| `POST /api/clusters/settings/actions/set/{setting}/{value}` | Set a global setting value |
| `POST /api/clusters/settings/actions/clear/{setting}` | Clear a global setting |

This is an alternative to the existing `global-settings` grant. Operators can assign `global-admin-config` to give monitoring-configuration write access without the full `global-settings` privilege.

---

## ACL shorthand expansion

The `global` shorthand in `api-credentials-acl-allow` expands to all grants returned by `config.GetGrantGlobal()`:

```
global-grant
global-settings
global-admin-show
global-admin-config
```

**Example ACL giving a monitoring-only account read access to the global dashboard:**

```toml
api-credentials-acl-allow = "admin:cluster db proxy prov global grant show sale extrole terminal,monitor:global-admin-show show"
```

---

## Example: viewer account with job tab access

A read-only viewer that can see the cluster dashboard including the Jobs tab:

```toml
api-credentials-acl-allow = "viewer:show cluster-show cluster-show-jobs cluster-show-backups"
```

| Grant | Effect |
|---|---|
| `show` | Basic dashboard access |
| `cluster-show` | Cluster topology and status |
| `cluster-show-jobs` | Job results tab (read-only; no cancel/submit) |
| `cluster-show-backups` | Backup list tab (read-only) |

---

## Security log file rotation

`SecurityLogrus` uses the same `RotateFileHook` infrastructure as the main log. The log level for security events is always `WARN` or higher — security events are never suppressed by the `log-file-level` setting of the main log.

---

## Code locations

| File | Responsibility |
|---|---|
| `cluster/cluster_acl.go` | `logUserGrantAssignment()`, `LoadAPIUsers()` |
| `cluster/cluster_add.go` | `AddUser()`, `UpdateUser()`, `DropUser()` security calls |
| `cluster/cluster_acl_rules.go` | `globalSettingsACLRules` (includes `global-admin-config`) |
| `config/config.go` | Grant constants, `GetGrantGlobal()`, `MonitoringLogAPILogin`, `MonitoringLogAPILoginSilentUsers` |
| `server/server.go` | `SecurityLogrus` init, `logSecurityEvent()`, `isAPILoginSilenced()` |
| `server/api.go` | `loginHandler()` success path, `UserHasGlobalGrant()` |
| `server/api_database.go` | `secretLoginHandler()` success path |
| `server/api_global.go` | Grant guard on `/api/global/*` handlers |
| `server/api_cluster.go` | `switchRepmanSetting()`, `setRepmanSetting()` for new settings |
| `share/dashboard_react/src/Pages/ClustersGlobalSettings/GlobalSettings.jsx` | GUI controls for login logging settings |
