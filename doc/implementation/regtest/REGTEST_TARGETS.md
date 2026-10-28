# Regtest targets

Issue #2198.

## Why

A regtest result was PASS or FAIL only. A test that did not apply to the cluster had to pick one, and the tests
disagreed: some returned PASS after logging "Skipping", the group replication gate returned FAIL on every
master-slave cluster. Since the results are reported per setup to the Cloud18 CRM (tested setups, #2165), a
skipped PASS marked a setup tested when nothing ran, and a skipped FAIL marked a healthy setup failing.

## Design

`regtest/regtest.go`:

- `TestTarget` holds one set of conditions, all required (AND). An empty field (0 for a number, nil for
  a map) means any.

  | Field | Name in logs and docs | Read from |
  |---|---|---|
  | `TopologyTarget []string` | topology-target | `cl.GetTopologyTarget()`: the declared topology, known before discovery and stable while a test breaks the cluster |
  | `NumberNodesTarget int` | number-nodes-target | `len(cl.GetServers())`, a minimum |
  | `FlavorTarget []string` | flavor-target | the version of `SetupServer(cl)`, lowercased, as `regtestSetupOf` reports it |
  | `OrchestratorTarget []string` | orchestrator-target | `cl.GetOrchestrator()` |
  | `DbuTarget int` | dbu-target | `cl.GetPlanDbu() / nodes`, a minimum per node |
  | `ConfigVariableMustHave map[string]string` | config-variable-must-have | `cl.Conf.GetValueByKey(key)`, compared case-insensitively: checked, never set |
  | `ConfigVariableMustSet map[string]string` | config-variable-must-set | set by the runner before the test, not a condition |

- `testTargets` maps a test name to a list of `TestTarget`: the test runs when the cluster matches at
  least one of them (OR). One list per test, not one target, because the other conditions depend on
  the topology: a switchover can apply to master-slave from 2 nodes and to Galera from 3 nodes on
  MariaDB only.
- Every test of the `tests` list declares its targets, and every key of `testTargets` is a test of the
  list (`TestTargetsDeclared`): a test added or renamed without them fails the unit tests. `anyCluster`
  (one empty target) marks a test that runs on every cluster; the tests not classified yet carry it, and
  get real targets as they are worked on.
- `SkipReason(cl, name)` returns empty when one target matches, otherwise the miss of each target
  (`missReason`, the first condition it misses) joined by `; `. The configuration variables are checked
  in key order, so the reason is the same at every run.
  An unknown flavor (no version read) or DBU (no plan) does not skip: the test runs and its own result says what
  it found, so a down cluster is a FAIL, not a SKIP that would hide it.
- `TestResultSkip = "SKIP"`, returned by `GetTestSkipLabel()` (the runner shadows the package name with its
  `regtest` variable, as for `GetTestResultLabel`).

`server/regtest.go` `RunAllTests` calls `MatchTarget` once the cluster is ready (after the suite's
`InitTestCluster`, after `SaveTestConf` on a live cluster), before the dispatch. A skipped test is logged
`Skip <test>: <reason>`, kept in `tests.json` with `SKIP` and the reason in `skip-reason`
(`cluster.Test.SkipReason`), its cluster closed or its configuration restored as for
a test that ran, and it is not appended to the CRM report.

`config.(*Config).GetValueByKey` reads a key through `configField`, the key-to-field index the configuration
archive already builds (T2), and prints it as the archive stores it (`true`, `4`, `opensvc`).

## Must have, must set

Two kinds of configuration variables, both declared per target:

- **Must have**: a setting that describes the architecture (`arbitration-external`, `topology-staging`,
  `prov-db-start-fetch-config`). Turning it on would not build what it describes (an arbitrator and a
  peer replication-manager, a staging cluster), so the runner only checks it: a cluster without it
  gives SKIP. A cluster that declares it but whose architecture does not work (the arbitrator does not
  answer) runs the test and FAILs: a broken cluster is reported, not hidden.
- **Must set**: a behaviour the test exercises (`check-replication-state`, `failover-max-slave-delay`,
  the rejoin modes). After the match, `setTestVariables` sets them in key order through
  `setClusterSetting`, the path of the API and the GUI, so the test runs with what an operator would
  set; `closeTest` restores them (`RestoreConf`, which also saves over a TOML the saver may have
  written, or the suite's cluster close). A key setClusterSetting refuses leaves the test unrun: ERR.

The order for each test: check the must-have (SKIP) → set the must-set (ERR) → run → restore. The
check reads the configuration before the test's own code runs, so a must-have never names a value the
test sets itself.

What stays in a test's code: runtime state that is no configuration key (`SetFailoverCtr`, the
failover counter; `SetBenchMethod`, an internal field), and a value changed in the middle of the
scenario (`TestBackupArchiveRemoveOriginal` picks `backup-physical-type` from the master's flavor).
A scan of the tests found no other change after the first action. 48 tests set configuration values at
their start; they move to must-set as they are worked on.

## Who reads SKIP

- The CRM report: a skipped test is not sent (not a tested setup).
- The CLI `--convert` HTML report: SKIP is grey with its reason; FAIL and ERR red, PASS green.
- The dashboard (Run a regression test): the API answers 200 whatever the result, and the banner said
  "ran successfuly" even for a FAIL. It now follows the result: PASS success, SKIP warning with the
  reason, anything else error.

## Targets vs runtime preconditions

A target is a fact of the setup: topology, size, flavor, orchestrator, plan, configuration. A test still checks
the live state it needs (a master discovered, a replica running, a proxy attached) and FAILs when it is missing:
on a cluster of the right setup, a missing replica is a broken cluster, not a test that does not apply.

## Gates moved to targets

- the group replication gate (topology `multi-master-grprep`, flavors mysql/percona, 3 nodes for the primary loss)
- the Kubernetes tests (orchestrator `kubernetes`) and `testOpenSVCUpgradeWarnRecovery` (orchestrator `opensvc`,
  2 nodes, `prov-db-start-fetch-config = true`); its "no slaves available" PASS is now a FAIL: a 2-node
  cluster without a replica is broken
- `testStagingRecoverNoReadOnly` (config `topology-staging = true`), now in the `ALL` list
- `testSwitchoverAllSlavesStopRplCheckNoSemiSync` (topology `master-slave`, 2 nodes; must set
  `check-replication-state = true`, `failover-max-slave-delay = 0`, which its code no longer sets)
- the split-brain tests (`testSetMinorityWithMaster`, `testSetMinorityWithMasterSysbench`,
  `testSetMinorityWithoutMaster`, `testSetDualActive`, `testSplitBrainNoWinner`): must have
  `arbitration-external = true` (`arbitrationRequired`). Their "arbitration not enabled → PASS" gate is
  gone: a cluster without arbitration gave a PASS where nothing ran

The other tests declare `anyCluster` for now; they get real targets as they are worked on.

## MySQL runs on GTID (#2226)

Before any target, `MatchTarget` skips every test on a MySQL or Percona cluster that does not declare the
`mysqlgtid` tag (`prov-db-tags`), group replication excepted (GTID comes from its own tag). The
provisioning adds the tag to every MySQL/Percona cluster it builds (`Cluster.addMySQLGTIDTag`), so a
cluster without it is not what a client runs: there a failover is positional, the replicas wait in
maintenance on the dead master, and a run on it reported failures of a setup nobody provisions.

## The table a consistency check reads (#2228)

The tests that end with `CheckTableConsistency` checked `test.sbtest`, a table nothing writes, while
sysbench writes `replication_manager_schema.sbtest1` (`--mysql-db`). Two NULL checksums used to compare
equal, so those tests passed without checking anything. Since a78c2f0a9, a table that cannot be
checksummed fails, so they all failed, whatever the failover did. They now read `Cluster.BenchTable()`:
`replication_manager_schema.sbtest1` for sysbench, `replication_manager_schema.bench` for the table
benchmark.

sysbench does not create its database. `PrepareBench` now creates `replication_manager_schema` on the
master first (`dbhelper.CreateReplicationManagerSchema`), with the binary log on so the replicas get it.
On a MySQL cluster nothing else had created it, and every prepare failed with error 1049.

`CheckTableConsistency` first waits for every replica taking part (`replicaTakesPart`: not stopped,
broken or delayed) to reach the master's position, read once
(`waitReplicaReachTarget`: `WaitSyncToTarget` turns, ended by an SQL error or no progress for
`replicaNoProgressFor`, the loop the restore gate uses too). Compared at once, a replica still applying
the 1,000,000-row prepare differed (MariaDB 11.8: 693,504 rows applied). The flashback and mysqldump
failover tests run `RunBench` on the table they prepared, as the other failover tests do: `RunSysbench`
cleans up and prepares again, which raced the first prepare (error 1050).

## Tests

`regtest/regtest_targets_test.go`: `TestSkipReason` covers each condition, matched and missed, the unknown
DBU, and a test with two targets (either matches; both missed gives both reasons); `TestTargetsDeclared`
checks every test declares targets and every declaration names a test; `server/regtest_must_set_test.go`
checks the must-set goes through setClusterSetting and an unknown key stops the test;
`TestConfigVariableKeys` checks every must-have and must-set key exists, since a typo would skip its test
on every cluster. `TestMySQLClusterDeclaresGTIDTag` covers the MySQL GTID tag gate.
`TestConsistencyChecksUseBenchTable` refuses a regtest that checks a literal table instead of
`BenchTable()`.
