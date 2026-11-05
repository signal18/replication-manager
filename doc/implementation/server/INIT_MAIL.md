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
  flag itself is rebuilt later (`RefreshGlobalInterventionState`), so a global intervention reaches the
  mail through its per-cluster entries.
- Counted, not hidden: the skipped mail is one suppressed alert on each muted cluster
  (`IncrementSuppressedAlerts`), shown in the intervention's summary, and logged at INFO.
- One cluster not muted: the mail goes, as before. `mail-to` empty: no mail, as before.

Test: `server/server_mail_init_test.go`.
