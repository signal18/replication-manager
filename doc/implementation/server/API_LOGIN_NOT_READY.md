# API login while starting: 503, never 401 (2026-09-29)

**Incident, preprod, twice in one morning.** The active repman was restarted. Its HTTP
listener opened at 11:39:19 (and 11:55:32); one second later the standby peer, reconnecting at
split resolve, logged in once per cluster with the admin credentials. No cluster was
initialised yet, so no ACL user existed, and `loginHandler` refused those logins as
*invalid credentials*. Three of them tripped the lock, three attempts per username, and `admin`
was locked for every client for three minutes, the dashboard included. The monitoring scripts
that were first blamed produced no failure at all.

**Root cause.** The listener starts before the clusters' init phases (`server.go`, `Run`), so a
login arriving in that window is judged against an empty cluster list and called a bad
password.

**Fix.** `ReplicationManager.clustersReady` (atomic) is set right after phase 4, once every
cluster's `Init`, `LoadAPIUsers` included, is done. Until then `loginHandler` answers
**503 Service Unavailable** with `Retry-After: 5` and returns before touching the attempt
counter. Nothing can count it as a failure. Test: `TestLoginNotReadyAnswers503AndCountsNothing`.

**Deliberately not changed** (Stéphane, 2026-09-29: "the rest can have dramatic consequences"):
the attempt lock itself (three attempts per username) and the standby's split-resolve prefetch
(one login per cluster, no back-off) stay as they are; a per-client failure limiter was
prototyped and set aside.
