# Restart mail ("Replication-Manager started on …")

`SendClustersInitMail` (`server/server_mail.go`) mails `mail-to` once per start, after every cluster
is initialised (`server.go`, after `clustersReady`): the start itself, and a summary of the clusters.

## Gated by the mute

The mail is not sent when the mute covers every cluster (`initMailMuted`):

- a global intervention (`/api/actions/intervention-start`, `IsGlobalIntervention`), or
- each cluster under an intervention (`/api/clusters/{cluster}/actions/intervention-start`,
  `IsIntervention`) or the scheduler's alert disable (`IsAlertDisable`),

the same gates as the cluster alerts (`cluster_chk.go`). An intervention exists so that a planned
series of restarts and tests mails no one; before, every restart during it still sent this mail.

- Across the restart: the interventions are restored from each cluster's `interventions.json` when the
  cluster initialises (`LoadInterventionHistory`, `cluster.go`), before the mail is sent. The global
  flag itself is rebuilt later (`RefreshGlobalInterventionState`), so at a start a global intervention
  reaches the mail through its per-cluster entries; the global flag is read for a mail sent while
  running. The unit test sets the flag by hand; the restore-before-mail order is checked on a real
  restart with an intervention in place.
- Counted, not hidden: the skipped mail is one suppressed alert on each cluster under an intervention
  (`IncrementSuppressedAlerts`, shown in the intervention's summary), and logged at INFO. A cluster muted
  by the scheduler's alert disable alone has no counter: only the log line says it.
- The cluster map is read under `repman.Lock()`, as `StartCluster` and `DeleteCluster` write it.
- One cluster not muted: the mail goes, as before. `mail-to` empty: no mail, as before.
- An intervention restored with its `AutoEndAt` already past still mutes until the first scheduler tick
  ends it: the restart mail sent in that window is skipped, as the alerts are.

Test: `server/server_mail_init_test.go`.
