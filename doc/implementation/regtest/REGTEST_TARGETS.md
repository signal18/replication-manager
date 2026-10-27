# Regtest targets

Issue #2198.

## Why

A regtest result was PASS or FAIL only. A test that did not apply to the cluster had to pick one, and the tests
disagreed: some returned PASS after logging "Skipping", the group replication gate returned FAIL on every
master-slave cluster. Since the results are reported per setup to the Cloud18 CRM (tested setups, #2165), a
skipped PASS marked a setup tested when nothing ran, and a skipped FAIL marked a healthy setup failing.

## Design

`regtest/regtest.go`:

- `TestTarget` holds the targets of a test. An empty field (0 for a number, nil for a map) means any.

  | Field | Name in logs and docs | Read from |
  |---|---|---|
  | `TopologyTarget []string` | topology-target | `cl.GetTopologyTarget()`: the declared topology, known before discovery and stable while a test breaks the cluster |
  | `NumberNodesTarget int` | number-nodes-target | `len(cl.GetServers())`, a minimum |
  | `FlavorTarget []string` | flavor-target | the version of `SetupServer(cl)`, lowercased, as `regtestSetupOf` reports it |
  | `OrchestratorTarget []string` | orchestrator-target | `cl.GetOrchestrator()` |
  | `DbuTarget int` | dbu-target | `cl.GetPlanDbu() / nodes`, a minimum per node |
  | `ConfigVariableTarget map[string]string` | config-variable-target | `cl.Conf.GetValueByKey(key)`, compared case-insensitively |

- `testTargets` maps a test name to its targets, next to the `tests` list. A test not in the map has none.
- `SkipReason(cl, name)` returns the first target the cluster misses, empty when it matches.
  An unknown flavor (no version read) or DBU (no plan) does not skip: the test runs and its own result says what
  it found, so a down cluster is a FAIL, not a SKIP that would hide it.
- `TestResultSkip = "SKIP"`, returned by `GetTestSkipLabel()` (the runner shadows the package name with its
  `regtest` variable, as for `GetTestResultLabel`).

`server/regtest.go` `RunAllTests` calls `SkipReason` once the cluster is ready (after the suite's
`InitTestCluster`, after `SaveTestConf` on a live cluster), before the dispatch. A skipped test is logged
`Skip <test>: <reason>`, kept in `tests.json` with `SKIP`, its cluster closed or its configuration restored as for
a test that ran, and it is not appended to the CRM report.

`config.(*Config).GetValueByKey` reads a key through `configField`, the key-to-field index the configuration
archive already builds (T2), and prints it as the archive stores it (`true`, `4`, `opensvc`).

## Targets vs runtime preconditions

A target is a fact of the setup: topology, size, flavor, orchestrator, plan, configuration. A test still checks
the live state it needs (a master discovered, a replica running, a proxy attached) and FAILs when it is missing:
on a cluster of the right setup, a missing replica is a broken cluster, not a test that does not apply.

## Gates moved to targets

- the group replication gate (topology `multi-master-grprep`, flavors mysql/percona, 3 nodes for the primary loss)
- the Kubernetes tests (orchestrator `kubernetes`) and `testOpenSVCUpgradeWarnRecovery` (orchestrator `opensvc`)
- `testStagingRecoverNoReadOnly` (config `topology-staging = true`), now in the `ALL` list
- `testSwitchoverAllSlavesStopRplCheckNoSemiSync` (topology `master-slave`, 2 nodes)

The other tests have no targets yet; they get them as they are worked on.

## Tests

`regtest/regtest_targets_test.go`: `TestSkipReason` covers each target, matched and missed, and the unknown DBU;
`TestConfigVariableTargetKeys` checks every declared configuration key exists, since a typo would skip its test
on every cluster.
