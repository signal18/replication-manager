# Standby imports the clusters created on the active (#1946)

## Problem
A standby got the config of the clusters it already knew from its active peer (GINF001, "standby pulling config from active peer"). Its periodic git pull only fetches the BO `-pull` repository (peers, partners, plugins). The shared config repository (`git-url`), where the active pushes a new cluster, was cloned only at startup (restore) or by the manual admin action `POST /api/clusters/actions/fetch-dynamic-from-git`. A cluster created on the active after the standby started therefore never reached it. On preprod on 2026-10-09, DR monitored 10 of the active's 14 clusters: a takeover would have left tamarin and the three PostgreSQL clusters without a monitor.

## Behaviour
After its pull cycle (`PullCloud18Configs`), `maybeImportClustersOnStandby` (`server/server_standby_import.go`) runs `FetchDynamicClustersFromGit`. That is the same import as the manual action: it clones the main repository to a staging directory and starts each cluster found there that is not known locally. It never overwrites an existing cluster.

Conditions (`standbyImportDue`):
- `cloud18 = true`, `arbitration-external = true`, `git-config-sync-standby = true` (default), and a `git-url`;
- the instance status is `S` (read under the repman lock);
- at most once every 10 minutes (`standbyImportInterval`, counted from the start of the last run, so a failed run also waits 10 minutes);
- one run at a time (`inFlight`).

The run happens in the background. It rechecks that the instance is still a standby right before cloning, so a takeover in between imports nothing. Imported clusters and per-cluster errors are logged in the git module.

## Notes
- The import is called after the `-pull` block, so it does not depend on `git-url-pull`. It does depend on the pull goroutine's cycle.
- An imported cluster starts in standby with its own config from the repository. Compare its `failover-mode` with the active's: on 2026-10-07 a standby set to automatic failed crm over while the active was manual.
- Tests: `server/server_standby_import_test.go` covers the conditions, throttling and single flight.
