# Slideshow Feature

## Overview

The slideshow page (`/slideshow`) cycles automatically through all monitored clusters and their dashboard sections, designed for NOC screens or unattended display.

## Route

`/slideshow` — accessible to authenticated users; viewer tokens (auto-login from `/dashboard`) are supported.

## Slide Structure

Each slide is a `{ clusterName, view, label }` tuple. Slides are built from the cluster list in this fixed section order per cluster:

| View key | Label | Grant required |
|---|---|---|
| `cluster-ha` | Cluster & HA | *(any authenticated user)* |
| `servers-proxies` | Servers & Proxies | *(any authenticated user)* |
| `servers-detail` | Server Details | *(any authenticated user)* |
| `apps` | Application Servers | *(any, cluster must have app servers)* |
| `logs-cluster` | Cluster & Security Logs | *(any authenticated user)* |
| `logs-workload` | Workload Logs | *(any authenticated user)* |
| `maintenance-backup` | Backup | `cluster-show-backups` |
| `maintenance-jobs` | Scheduler Jobs | `cluster-show-jobs` or `cluster-process` |
| `logs-jobs` | Job Logs | `cluster-show-jobs` or `cluster-process` |

### Conditional inclusion

- `maintenance-backup`, `maintenance-jobs`, `logs-jobs` are skipped when the cluster has no backup or scheduler configured (`backupRestic`, `backupPhysicalType`, `backupLogicalType`, `schedulerEnabled`).
- `apps` is skipped when the cluster has no app servers.
- `maintenance-backup` is skipped when the user lacks `cluster-show-backups`.
- `maintenance-jobs` and `logs-jobs` are skipped when the user lacks both `cluster-show-jobs` and `cluster-process`.
- When the user object is not yet loaded (initial state), all structurally eligible slides are shown optimistically.

## Slide Duration

`SLIDE_DURATION_MS = 15000` (15 seconds per slide). Configurable at build time by editing the constant in `src/Pages/Slideshow/index.jsx`.

## Progress Bar

A thin bar under the header advances from 0 → 100 % over each slide's duration. It turns **gray** when the slideshow is paused.

## Stop / Start

A **Stop/Start button** (HiStop / HiPlay icons) appears in the header right side. When paused:
- The slide ticker freezes (progress bar stops advancing, no slide transitions).
- The background data poller continues running so data stays fresh when resumed.

## Auto-refresh Controls

The navbar `RefreshCounter` (manual reload button + interval input) is **hidden** on the `/slideshow` route. The slideshow manages its own data polling independently.

## Data Loading

Two independent intervals:

| Interval | Behavior |
|---|---|
| **Slide ticker** (250 ms tick) | Advances progress, transitions to next slide, calls `loadSlideData` on transition. Respects pause state. |
| **Background poller** (`refresh_interval` seconds) | Calls `getClusters` + `loadSlideData` for current slide on every tick. Always runs, even when paused. |

`loadSlideData` dispatches:
- `getClusterData`, `getClusterServers`, `getClusterProxies`, `getClusterAlerts`, `getClusterMaster`, `getClusterLogs`, `getClusterApps` — for every slide.
- `getResticSnapshot`, `getBackups`, `getBackupStats`, `getResticCurrentTask`, `getJobs` — only for `maintenance-*` and `logs-jobs` slides.

A fast retrier (5 s) runs until clusters are loaded, to handle server restarts.

## Required Grants for Viewer Users

A typical read-only viewer configured with `show cluster-show` needs the following additional grants to see all slideshow sections:

```
cluster-show-backups   # to see Backup slide
cluster-show-jobs      # to see Scheduler Jobs and Job Logs slides
```

Example ACL entry in `config.toml`:
```
api-credentials-acl-allow = "viewer:show cluster-show cluster-show-backups cluster-show-jobs"
```

## Grant: `cluster-show-jobs`

**Constant**: `config.GrantClusterShowJobs = "cluster-show-jobs"`

**ACL rule** (`cluster/cluster_acl_rules.go`):
```go
{"/jobs", nil, []string{config.GrantClusterProcess, config.GrantClusterShowJobs}},
```

This gives read-only access to `GET /api/clusters/{name}/jobs`. Write operations on jobs (cancel) still require `cluster-process`.

## Authentication

The slideshow uses the same JWT token mechanism as the dashboard. The `/dashboard` route provides auto-login for viewer tokens (no interactive login required for NOC display).

PageContainer redirects unauthenticated `/slideshow` requests to `/dashboard` (not `/login`) so that viewer auto-login is triggered instead of the regular login form.
