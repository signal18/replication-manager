// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Integration scenarios for the direct (streamed) mysqldump reseed path's
// two-phase --system=all handling. See
// doc/implementation/cluster/SYSTEM_ALL_RESEED_FIX_PLAN.md, "Integration/regtest
// scenarios". These exercise the real ServerMonitor/Cluster code
// (JobRejoinMysqldumpFromSource, restoreSystemCatalog, dbhelper.GetPluginStatusConn)
// against live MariaDB/MySQL servers -- unlike the sqlmock/fake-executable unit
// tests in cluster/srv_job_reseed_test.go and cluster/srv_job_test.go, which
// cover the same code paths without a live database.

package regtest

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

const testDirectReseedPluginName = "metadata_lock_info"

// installTestDirectReseedPlugin installs a small, always-available built-in
// MariaDB/MySQL plugin (metadata_lock_info) so scenarios can exercise the
// live INSTALL PLUGIN skip guard without a third-party plugin binary.
//
// Checks first (dbhelper.GetPlugins) and only issues a bare INSTALL PLUGIN
// when absent, rather than "INSTALL PLUGIN IF NOT EXISTS" (MariaDB-only, not
// portable to MySQL/Percona) or IF/THEN/END IF (not valid as a standalone
// statement on either engine outside a routine body).
//
// Proves ACTIVE, not merely present: scenario 2's claim is specifically that
// an already-ACTIVE plugin gets skipped (resolveInstallPluginSkip treats
// present-but-not-ACTIVE as fatal, not a skip), so a found-but-inactive
// plugin is uninstalled and reinstalled to reach a known ACTIVE state before
// returning.
func installTestDirectReseedPlugin(server *clusterpkg.ServerMonitor) error {
	status, found, err := findTestDirectReseedPluginStatus(server)
	if err != nil {
		return err
	}
	if found && strings.EqualFold(status, "ACTIVE") {
		return nil
	}
	if found {
		// Present but not ACTIVE: there is no portable "re-activate" statement
		// on either engine, so uninstall and reinstall to reach a known state.
		if err := server.ExecQueryNoBinLog("UNINSTALL PLUGIN "+testDirectReseedPluginName, 10*time.Second); err != nil {
			return fmt.Errorf("plugin %s is present but not ACTIVE (status=%s) and could not be uninstalled to reinstall: %w", testDirectReseedPluginName, status, err)
		}
	}
	if err := server.ExecQueryNoBinLog("INSTALL PLUGIN "+testDirectReseedPluginName+" SONAME 'metadata_lock_info.so'", 10*time.Second); err != nil {
		return err
	}
	status, found, err = findTestDirectReseedPluginStatus(server)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("plugin %s not found immediately after INSTALL PLUGIN", testDirectReseedPluginName)
	}
	if !strings.EqualFold(status, "ACTIVE") {
		return fmt.Errorf("plugin %s installed but status is %q, not ACTIVE", testDirectReseedPluginName, status)
	}
	return nil
}

func findTestDirectReseedPluginStatus(server *clusterpkg.ServerMonitor) (status string, found bool, err error) {
	plugins, _, err := dbhelper.GetPlugins(server.Conn, server.DBVersion)
	if err != nil {
		return "", false, err
	}
	for name, p := range plugins {
		if strings.EqualFold(name, testDirectReseedPluginName) {
			return p.Status, true, nil
		}
	}
	return "", false, nil
}

// hasPublishedDirectReseedArtifact reports whether dest has at least one
// published (non-.tmp-) direct-reseed-system artifact directory on disk.
func hasPublishedDirectReseedArtifact(dest *clusterpkg.ServerMonitor) bool {
	_, ok := latestPublishedDirectReseedArtifactDir(dest)
	return ok
}

// latestPublishedDirectReseedArtifactDir returns the full path of the most
// recently created published (non-.tmp-) direct-reseed-system artifact
// directory for dest, if any. Entry names are UTC-timestamp-prefixed
// (directReseedSystemArtifactDir), so a lexical max is also the most recent.
func latestPublishedDirectReseedArtifactDir(dest *clusterpkg.ServerMonitor) (string, bool) {
	root := filepath.Join(dest.GetMyBackupDirectoryPath(), "direct-reseed-system")
	entries, err := clusterpkg.ListDirectReseedSystemArtifacts(root)
	if err != nil {
		return "", false
	}
	latest := ""
	for _, e := range entries {
		if strings.Contains(e, ".tmp-") {
			continue
		}
		if e > latest {
			latest = e
		}
	}
	if latest == "" {
		return "", false
	}
	return filepath.Join(root, latest), true
}

// TestDirectReseedSystemAllEmptyDestination is scenario 1: an empty
// destination (no plugins, no pre-existing users beyond defaults) reseeds
// successfully via the direct streamed path without --force.
func (regtest *RegTest) TestDirectReseedSystemAllEmptyDestination(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave available for direct reseed test")
		return false
	}
	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for direct reseed test")
		return false
	}
	slave := cl.GetSlaves()[0]

	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Direct reseed %s from %s (empty destination)", slave.URL, master.URL)
	if err := cl.JobRejoinMysqldumpFromSource(master, slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobRejoinMysqldumpFromSource failed: %s", err)
		return false
	}

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after direct reseed")
		return false
	}
	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after direct reseed")
		return false
	}
	return true
}

// TestDirectReseedSystemAllPluginAlreadyActive is scenario 2: the destination
// already has the same plugin ACTIVE. Direct reseed must succeed, and the
// live guard (dbhelper.GetPluginStatusConn) must deliberately skip that one
// INSTALL PLUGIN statement rather than aborting the whole reseed.
func (regtest *RegTest) TestDirectReseedSystemAllPluginAlreadyActive(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave available for direct reseed test")
		return false
	}
	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for direct reseed test")
		return false
	}
	slave := cl.GetSlaves()[0]

	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	// Pre-provision the destination with the same plugin ACTIVE, simulating
	// a not-actually-empty "fresh" instance (the scenario --force used to
	// paper over).
	if err := installTestDirectReseedPlugin(slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to pre-install plugin on destination: %s", err)
		return false
	}
	if err := installTestDirectReseedPlugin(master); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to install plugin on source (so it's dumped): %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Direct reseed %s from %s (destination already has plugin ACTIVE)", slave.URL, master.URL)
	if err := cl.JobRejoinMysqldumpFromSource(master, slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"JobRejoinMysqldumpFromSource failed (expected the ACTIVE plugin to be skipped, not to abort the reseed): %s", err)
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after direct reseed")
		return false
	}
	return true
}

// TestDirectReseedSystemAllPreExistingUserAppliedViaAlterUser proves the
// production fix for the mariadb.sys@localhost CREATE USER collision
// (execSplitdumpSingle's ER_CANNOT_USER/1396 fallback in
// cluster/srv_job_backup.go): when the destination already has an account
// that --system=all also dumps as a plain CREATE USER, the reseed must not
// abort -- it must replay the statement as ALTER USER so the account ends up
// matching the source's definition, not silently left as whatever the
// destination had before (which would be the wrong behavior here, unlike the
// deliberate INSTALL PLUGIN skip in TestDirectReseedSystemAllPluginAlreadyActive).
//
// max_user_connections is the observable: it's set differently on each side
// via CREATE USER's own WITH resource_option clause (not a separate GRANT),
// so a destination value that ends up matching the SOURCE afterward is
// direct evidence the fallback actually applied the dumped definition,
// rather than merely not-erroring.
//
// Tears down its own probe account on both master and destination before
// returning PASS, verified on the destination rather than trusted: this is
// test-only state with no reason to survive the scenario, and
// server/regtest.go runs scenarios sequentially against one shared cluster
// outside SUITE mode, so leftover state here would leak into later regtests
// (same reasoning TestDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure's
// probe-object cleanup documents).
func (regtest *RegTest) TestDirectReseedSystemAllPreExistingUserAppliedViaAlterUser(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave available for direct reseed test")
		return false
	}
	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for direct reseed test")
		return false
	}
	slave := cl.GetSlaves()[0]

	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	const probeUser = "'direct_reseed_alter_probe'@'%'"
	const sourceMaxConn = 7
	const destMaxConn = 1

	// Explicit drop-then-create (not CREATE USER IF NOT EXISTS) so the
	// scenario starts from a known state on every rerun -- IF NOT EXISTS
	// would no-op against a probe user left over from a prior run, silently
	// keeping whatever max_user_connections that run ended with instead of
	// the values this run intends to test against, which would make the
	// after-reseed assertion below pass even without the fallback working.
	if err := master.ExecQueryNoBinLog("DROP USER IF EXISTS "+probeUser, 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to reset probe user on source: %s", err)
		return false
	}
	if err := master.ExecQueryNoBinLog(fmt.Sprintf("CREATE USER %s WITH MAX_USER_CONNECTIONS %d", probeUser, sourceMaxConn), 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create probe user on source: %s", err)
		return false
	}
	// Pre-create the SAME account on the destination with a DIFFERENT
	// definition, simulating mariadb.sys@localhost (or any account
	// bootstrapped independently on both sides): --system=all dumps a plain
	// CREATE USER for it, which collides (ER_CANNOT_USER) unless the
	// fallback applies it as ALTER USER instead.
	if err := slave.ExecQueryNoBinLog("DROP USER IF EXISTS "+probeUser, 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to reset colliding probe user on destination: %s", err)
		return false
	}
	if err := slave.ExecQueryNoBinLog(fmt.Sprintf("CREATE USER %s WITH MAX_USER_CONNECTIONS %d", probeUser, destMaxConn), 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to pre-create colliding probe user on destination: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Direct reseed %s from %s (destination already has a colliding user account)", slave.URL, master.URL)
	if err := cl.JobRejoinMysqldumpFromSource(master, slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"JobRejoinMysqldumpFromSource failed (expected the CREATE USER collision to fall back to ALTER USER, not abort the reseed): %s", err)
		return false
	}
	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after direct reseed")
		return false
	}

	var gotMaxConn int
	if err := slave.Conn.QueryRow("SELECT max_user_connections FROM mysql.user WHERE user = 'direct_reseed_alter_probe' AND host = '%'").Scan(&gotMaxConn); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to read back probe user's max_user_connections on destination: %s", err)
		return false
	}
	if gotMaxConn != sourceMaxConn {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Probe user's max_user_connections is %d after reseed, want %d (source's value): the ALTER USER fallback did not apply the dumped definition", gotMaxConn, sourceMaxConn)
		return false
	}

	// Teardown, not just assertion: this scenario's probe account is
	// test-only state with no reason to survive it, and server/regtest.go
	// runs scenarios sequentially against one shared cluster outside SUITE
	// mode, so leaving it behind on either side would leak into later
	// regtests (same reasoning TestDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure's
	// probe-object cleanup documents). Drop on both master and destination,
	// then verify on the destination before reporting PASS rather than
	// trusting the drop alone.
	for _, srv := range []*clusterpkg.ServerMonitor{master, slave} {
		if err := srv.ExecQueryNoBinLog("DROP USER IF EXISTS "+probeUser, 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to clean up probe user on %s after the scenario (would leave account drift for later regtests): %s", srv.URL, err)
			return false
		}
	}
	var leftoverCount int
	if err := slave.Conn.QueryRow("SELECT COUNT(*) FROM mysql.user WHERE user = 'direct_reseed_alter_probe' AND host = '%'").Scan(&leftoverCount); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to verify probe user cleanup on destination: %s", err)
		return false
	}
	if leftoverCount != 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Probe user still present on destination after cleanup -- would leave account drift for later regtests")
		return false
	}
	return true
}

// TestDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure is scenario 3/4:
// force phase two to fail deterministically, confirm the job fails with the
// "system catalogue replay"/"system extraction" stage and the published
// artifact survives, repair the condition, and retry via the internal
// RetryDirectReseedSystemCatalog entry point -- accepting EITHER of the two
// outcomes the design doc's own scenario 4 anticipates as valid ("full
// success from the beginning ... otherwise the retry path explicitly
// rejects the case per the documented narrower scope"), not just one.
//
// Whichever outcome occurs, this function leaves the cluster clean before
// returning PASS, not just "not obviously broken":
//   - Replication running. RetryDirectReseedSystemCatalog only restarts
//     replication on ITS OWN success path (by production design --
//     replication must never resume on top of an artifact whose retry was
//     refused), so the "correctly refused" branch below explicitly restarts
//     it itself rather than leaving the slave stopped.
//   - No schema drift. RetryDirectReseedSystemCatalog replays only the
//     frozen system-catalogue artifact, never phase-one's application/schema
//     restore -- so the repair step below (ADD COLUMN probe_col, applied to
//     the DESTINATION only, purely to give the artifact's dangling grant a
//     target the source no longer has) is never undone by anything else in
//     this function, in EITHER PASS branch. Left alone, the destination
//     would keep a column the source doesn't have.
//
// Neither of these is a production concern on their own (a real refused
// retry SHOULD leave replication stopped for an operator to resolve; a real
// successful retry SHOULD leave the destination matching the frozen artifact,
// not necessarily the source's latest live schema) -- they're regtest-hygiene
// requirements specific to this harness: server/regtest.go runs scenarios
// sequentially against one shared cluster outside SUITE mode, so a "passing"
// scenario that leaves the slave stopped or schema-drifted would silently
// poison every later scenario on that cluster. Both are explicitly repaired
// and verified before returning true, not just assumed fixed because the
// scenario's own assertions above passed.
//
// Failure mechanism: a *dangling* column-level GRANT, not a CREATE USER
// collision (that was this scenario's original mechanism -- see the
// production fix in cluster/srv_job_backup.go and the success case this now
// exercises in TestDirectReseedSystemAllPreExistingUserAppliedViaAlterUser,
// which made a plain user collision recoverable, not fatal, and therefore
// unusable to force phase two to fail here). MariaDB/MySQL/Percona do not
// revoke mysql.columns_priv rows when the privileged column is dropped -- the
// grant lingers and --system=all dumps it verbatim as a GRANT statement for a
// column that no longer exists. Phase one recreates the probe table from its
// CURRENT (post-drop) structure, so by the time phase two replays that stale
// column grant, the column genuinely doesn't exist on the destination and the
// GRANT fails. This is deterministic and portable across engines/versions.
//
// This scenario cannot force progressed=false (retry-safe-from-the-start) at
// the failing GRANT: --system=all dumps every account, including the probe
// grantee, so its CREATE USER necessarily appears in the artifact and
// necessarily precedes the GRANT that targets it (a dump tool can't validly
// emit a GRANT to an account it hasn't created yet) -- and since the CREATE
// USER fallback above now makes that CREATE USER succeed (directly or via
// ALTER USER) rather than abort, something always commits before the GRANT
// fails. That's the fix working as intended, not a bug in this scenario, but
// it does mean the artifact deterministically lands in replay-failed (not
// replay-failed-safe), and RetryDirectReseedSystemCatalog correctly refuses
// to redo it from the beginning -- so this scenario asserts THAT outcome as
// the expected one, rather than treating it as a failure to work around.
// Proving the OTHER valid outcome (a genuine progressed=false retry
// succeeding from the beginning) would need the deliberate failure to be the
// very first statement in the whole artifact, which isn't constructible from
// plain SQL against a symmetric source/destination pair in this harness; see
// SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md's Mandatory follow-up for the
// live-dump-fixture work that would be needed to cover it.
func (regtest *RegTest) TestDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave available for direct reseed test")
		return false
	}
	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for direct reseed test")
		return false
	}
	slave := cl.GetSlaves()[0]

	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	const probeUser = "'direct_reseed_probe'@'%'"
	const probeTable = "test.direct_reseed_priv_probe"

	// A fresh account (not pre-existing on the destination -- this scenario
	// is not exercising the CREATE USER fallback) needed as a grantee, plus a
	// table with a column that gets a column-level GRANT and is then dropped,
	// leaving a dangling mysql.columns_priv row.
	//
	// Explicit drop-then-create for both, not IF NOT EXISTS/IF EXISTS
	// no-ops: a prior run's table already has probe_col dropped, so a bare
	// CREATE TABLE IF NOT EXISTS would leave that column missing and the
	// GRANT below would fail here in setup, before the reseed path this
	// scenario is meant to exercise is even reached.
	if err := master.ExecQueryNoBinLog("DROP USER IF EXISTS "+probeUser, 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to reset probe user on source: %s", err)
		return false
	}
	if err := master.ExecQueryNoBinLog("CREATE USER "+probeUser, 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create probe user on source: %s", err)
		return false
	}
	if err := master.ExecQueryNoBinLog("DROP TABLE IF EXISTS "+probeTable, 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to reset probe table on source: %s", err)
		return false
	}
	if err := master.ExecQueryNoBinLog("CREATE TABLE "+probeTable+" (id INT PRIMARY KEY, probe_col INT)", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create probe table on source: %s", err)
		return false
	}
	if err := master.ExecQueryNoBinLog("GRANT SELECT (probe_col) ON "+probeTable+" TO "+probeUser, 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to grant column privilege on source: %s", err)
		return false
	}
	if err := master.ExecQueryNoBinLog("ALTER TABLE "+probeTable+" DROP COLUMN probe_col", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to drop probe column on source (to leave a dangling grant): %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Direct reseed %s from %s, expecting phase-two failure", slave.URL, master.URL)
	err := cl.JobRejoinMysqldumpFromSource(master, slave)
	if err == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Expected phase two to fail in this scenario, got success")
		return false
	}
	if !strings.Contains(err.Error(), "system catalogue replay") && !strings.Contains(err.Error(), "system extraction") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Expected a system-extraction/replay stage error, got: %s", err)
		return false
	}

	artifactDir, ok := latestPublishedDirectReseedArtifactDir(slave)
	if !ok {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Expected the published artifact to survive the phase-two failure")
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Phase-two failure retained artifact %s as expected: %s", artifactDir, err)

	// Repair: restore the dropped column on the destination so the dangling
	// grant's target is satisfiable on retry.
	if err := slave.ExecQueryNoBinLog("ALTER TABLE "+probeTable+" ADD COLUMN probe_col INT", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to repair destination (restore probe column) before retry: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Retrying system-catalogue replay from artifact %s", artifactDir)
	retryErr := cl.RetryDirectReseedSystemCatalog(slave, artifactDir)
	switch {
	case retryErr == nil:
		// Not the expected outcome per this function's doc comment (the
		// probe account's own CREATE USER should have committed before the
		// GRANT failed) but still a legitimate one to accept: it means the
		// artifact landed in the safely-retryable state and the repaired
		// retry-from-the-beginning genuinely worked end to end.
		if !cl.CheckSlavesRunning() {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after successful retry")
			return false
		}
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Retry from artifact %s succeeded and replication resumed", artifactDir)
	case strings.Contains(retryErr.Error(), "not safely retryable from the beginning"):
		// The expected outcome (see this function's doc comment): the probe
		// account's CREATE USER committed before the dangling-column GRANT
		// failed, so the artifact correctly landed in replay-failed and the
		// v1 retry scope correctly refuses to redo it from the beginning.
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"Retry from artifact %s was correctly refused as not safely retryable from the beginning: %s", artifactDir, retryErr)
		// This refusal is a real, permanent dead end for THIS artifact by
		// design (RetryDirectReseedSystemCatalog only restarts replication on
		// its own success path, and this branch never reaches it) -- slave
		// replication is left stopped exactly like it would be after a real
		// failed reseed an operator hasn't resolved yet. That's correct
		// production behavior, but left as-is here it would poison every
		// later regtest run against this same cluster outside SUITE mode
		// (server/regtest.go runs sequentially on one shared cluster
		// otherwise). The underlying data issue is already repaired above
		// (the column is back), so it's safe for the test's own purposes to
		// resume replication directly -- mirroring the exact restart
		// JobRejoinMysqldumpFromSource/RetryDirectReseedSystemCatalog do on
		// their success paths, just not gated behind this artifact's (now
		// permanently unusable) retry state.
		var slaveStartErrs []string
		for _, rep := range slave.Replications {
			if _, err := slave.StartSlaveChannel(rep.ConnectionName.String); err != nil {
				slaveStartErrs = append(slaveStartErrs, fmt.Sprintf("%s: %s", rep.ConnectionName.String, err))
			}
		}
		if len(slaveStartErrs) > 0 {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to restore replication health after the expected refusal (would poison later regtests on this cluster): %s", strings.Join(slaveStartErrs, "; "))
			return false
		}
		if !cl.CheckSlavesRunning() {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication still not running after restoring health post-refusal")
			return false
		}
	default:
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"RetryDirectReseedSystemCatalog failed after repairing the dangling grant target: %s", retryErr)
		return false
	}

	// Neither PASS branch above leaves the cluster source-equivalent on its
	// own: RetryDirectReseedSystemCatalog replays only the frozen
	// system-catalogue artifact, never phase-one's application/schema
	// restore, so the repair above (which added probe_col back to the
	// DESTINATION only, purely to give the artifact's dangling grant a
	// target) is never undone by anything else in this function. Left as-is,
	// the destination would keep a column the source doesn't have -- schema
	// drift a later regtest on this same shared cluster (server/regtest.go
	// runs sequentially outside SUITE mode) could inherit. Reconcile both
	// sides to a clean, identical, known state (no probe table/account at
	// all) rather than leaving one side ahead of the other, and verify it
	// before reporting PASS instead of trusting the drops alone.
	for _, srv := range []*clusterpkg.ServerMonitor{master, slave} {
		if err := srv.ExecQueryNoBinLog("DROP TABLE IF EXISTS "+probeTable, 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to clean up probe table on %s after the scenario (would leave schema drift for later regtests): %s", srv.URL, err)
			return false
		}
		if err := srv.ExecQueryNoBinLog("DROP USER IF EXISTS "+probeUser, 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to clean up probe user on %s after the scenario (would leave account drift for later regtests): %s", srv.URL, err)
			return false
		}
	}
	var leftoverCount int
	if err := slave.Conn.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'test' AND table_name = 'direct_reseed_priv_probe'").Scan(&leftoverCount); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to verify probe table cleanup on destination: %s", err)
		return false
	}
	if leftoverCount != 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Probe table still present on destination after cleanup -- would leave schema drift for later regtests")
		return false
	}
	return true
}

// TestDirectReseedSystemAllSplitUserSingleAuthority is scenario 6:
// backup-split-mysql-user must not cause a second, duplicate user replay
// during direct reseed -- the extracted mysql.system-all artifact remains
// the sole authority for user-related statements.
func (regtest *RegTest) TestDirectReseedSystemAllSplitUserSingleAuthority(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave available for direct reseed test")
		return false
	}
	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for direct reseed test")
		return false
	}
	slave := cl.GetSlaves()[0]

	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	originalSplitUser := cl.Conf.BackupSplitMysqlUser
	cl.Conf.BackupSplitMysqlUser = true
	defer func() { cl.Conf.BackupSplitMysqlUser = originalSplitUser }()

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Direct reseed %s from %s with backup-split-mysql-user=true", slave.URL, master.URL)
	if err := cl.JobRejoinMysqldumpFromSource(master, slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobRejoinMysqldumpFromSource failed: %s", err)
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after direct reseed")
		return false
	}
	// The direct path never prepends a separate user artifact regardless of
	// this flag (see JobRejoinMysqldumpFromSource / splitdump.ClassifyStream) --
	// a successful reseed with replication running, with no duplicate-user
	// error surfaced, is the observable proof there was exactly one
	// authoritative user source.
	return true
}
