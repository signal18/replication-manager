// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/releases"
)

// Replication TLS regtests (#1933, #1974, #1975, #1976). The matrix they cover was
// first measured by hand on dev3 (MariaDB 12.3.3):
//   - a primary on its auto-generated certificate is verified with no CA (11.4+
//     zero-configuration TLS, through the replication password);
//   - a CA that did not sign the primary's certificate cuts the channel (error 2026),
//     with or without certificate verification;
//   - the CA that signed it (the ssl tag's shipped CA) works, verification on.
// Each test puts the cluster back as it found it.

// tlsReplicationUpgradeLine is the release line the transparent-SSL upgrade test moves to.
const tlsReplicationUpgradeLine = "11.8"

func tlsLogf(cl *cluster.Cluster, format string, args ...interface{}) {
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
}

// tlsLiveVariable reads a global variable live ("" for NULL or unreadable).
func tlsLiveVariable(s *cluster.ServerMonitor, name string) string {
	if s == nil || s.Conn == nil || s.DBVersion == nil {
		return ""
	}
	v, _, err := dbhelper.GetVariableByNameNullable(s.Conn, name, s.DBVersion)
	if err != nil || !v.Valid {
		return ""
	}
	return v.String
}

// tlsChannel is the live replication status of the cluster channel on s.
func tlsChannel(cl *cluster.Cluster, s *cluster.ServerMonitor) (dbhelper.SlaveStatus, error) {
	if s.Conn == nil {
		return dbhelper.SlaveStatus{}, fmt.Errorf("no connection to %s", s.URL)
	}
	st, _, err := dbhelper.GetSlaveStatus(s.Conn, cl.Conf.MasterConn, s.DBVersion)
	return st, err
}

// tlsWait polls cond every 2 s until it returns nil or timeout passes.
func tlsWait(timeout time.Duration, cond func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := cond()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

// tlsSlavesReplicate waits until every slave's channel runs, its CA satisfying wantCA.
func tlsSlavesReplicate(cl *cluster.Cluster, timeout time.Duration, wantCA func(string) bool) error {
	return tlsWait(timeout, func() error {
		slaves := cl.GetSlaves()
		if len(slaves) == 0 {
			return fmt.Errorf("no slave")
		}
		for _, s := range slaves {
			st, err := tlsChannel(cl, s)
			if err != nil {
				return err
			}
			if st.SlaveIORunning.String != "Yes" || st.SlaveSQLRunning.String != "Yes" {
				return fmt.Errorf("%s not replicating: io %s sql %s, io error %s %s", s.URL, st.SlaveIORunning.String, st.SlaveSQLRunning.String, st.LastIOErrno.String, st.LastIOError.String)
			}
			if wantCA != nil && !wantCA(st.MasterSSLCAFile.String) {
				return fmt.Errorf("%s replicates with CA %q", s.URL, st.MasterSSLCAFile.String)
			}
		}
		return nil
	})
}

// tlsSecurityStateOpen reports whether the security state key is open on url ("" = any).
func tlsSecurityStateOpen(cl *cluster.Cluster, key, url string) bool {
	for _, st := range cl.SecurityStateMachine.GetOpenStates() {
		if st.ErrKey == key && (url == "" || st.ServerUrl == url) {
			return true
		}
	}
	return false
}

// tlsMonitoringKept fails when a server is declared failed, or a primary refuses
// plaintext connections, during d: the ssl tag must never cost the monitoring (#1974,
// #1975).
func tlsMonitoringKept(cl *cluster.Cluster, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, s := range cl.Servers {
			if s.IsFailed() {
				return fmt.Errorf("%s declared %s", s.URL, s.State)
			}
			if v := tlsLiveVariable(s, "REQUIRE_SECURE_TRANSPORT"); strings.EqualFold(v, "ON") {
				return fmt.Errorf("%s has require_secure_transport ON", s.URL)
			}
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

// tlsChangeMasterRaw changes the cluster channel of s by hand (what an operator or an
// earlier certificate left in master.info), outside replication-manager.
func tlsChangeMasterRaw(cl *cluster.Cluster, s *cluster.ServerMonitor, set string) error {
	ch := ""
	if cl.Conf.MasterConn != "" {
		ch = "'" + strings.ReplaceAll(cl.Conf.MasterConn, "'", "''") + "' "
	}
	for _, stmt := range []string{"STOP SLAVE " + ch, "CHANGE MASTER " + ch + "TO " + set, "START SLAVE " + ch} {
		if _, err := s.Conn.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %s", stmt, err)
		}
	}
	return nil
}

// tlsZeroConfig gates on MariaDB 11.4+ everywhere.
func tlsZeroConfig(cl *cluster.Cluster) bool {
	for _, s := range cl.Servers {
		if s.DBVersion == nil || !s.DBVersion.IsMariaDB() || !s.DBVersion.GreaterEqual("11.4") {
			return false
		}
	}
	return true
}

func tlsTopology(cl *cluster.Cluster) bool {
	return cl.GetMaster() != nil && len(cl.GetSlaves()) > 0
}

// TestReplicationTLSAutoCertificate: with replication-use-ssl on and the servers on their
// auto-generated certificates (MariaDB 11.4+, no ssl tag), two switchovers leave every
// replica encrypted, without CA, replicating, and no TLS warning open.
func (regtest *RegTest) TestReplicationTLSAutoCertificate(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !tlsTopology(cl) || !tlsZeroConfig(cl) || tlsLiveVariable(cl.GetMaster(), "SSL_CERT") != "" {
		tlsLogf(cl, "Skipping ReplicationTLSAutoCertificate: needs a MariaDB 11.4+ master-slave cluster on auto-generated certificates")
		return true
	}
	prev := cl.Conf.ReplicationSSL
	cl.Conf.ReplicationSSL = true
	defer func() { cl.Conf.ReplicationSSL = prev }()

	for i := 0; i < 2; i++ {
		cl.SwitchoverWaitTest()
	}
	if err := tlsSlavesReplicate(cl, 60*time.Second, func(ca string) bool { return ca == "" }); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSAutoCertificate: %s", err)
		return false
	}
	for _, s := range cl.GetSlaves() {
		st, _ := tlsChannel(cl, s)
		if st.MasterSSLAllowed.String != "Yes" {
			tlsLogf(cl, "FAIL ReplicationTLSAutoCertificate: %s not encrypted", s.URL)
			return false
		}
	}
	time.Sleep(10 * time.Second)
	if tlsSecurityStateOpen(cl, "WARN0235", "") || tlsSecurityStateOpen(cl, "WARN0236", "") {
		tlsLogf(cl, "FAIL ReplicationTLSAutoCertificate: a TLS warning is open on a zero-config verified cluster")
		return false
	}
	tlsLogf(cl, "PASS ReplicationTLSAutoCertificate")
	return true
}

// TestReplicationTLSStaleCAEmptied: a CA left in master.info that did not sign the
// primary's auto-generated certificate cuts the channel (WARN0236); the next CHANGE
// MASTER replication-manager issues empties it and the replica replicates again.
func (regtest *RegTest) TestReplicationTLSStaleCAEmptied(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !tlsTopology(cl) || !tlsZeroConfig(cl) || tlsLiveVariable(cl.GetMaster(), "SSL_CERT") != "" {
		tlsLogf(cl, "Skipping ReplicationTLSStaleCAEmptied: needs a MariaDB 11.4+ master-slave cluster on auto-generated certificates")
		return true
	}
	master, slave := cl.GetMaster(), cl.GetSlaves()[0]
	// Any CA file present on the replica that did not sign the primary's certificate.
	stale := "/etc/ssl/certs/ca-certificates.crt"
	if v := tlsLiveVariable(slave, "SSL_CA"); v != "" {
		stale = v
	}
	if err := tlsChangeMasterRaw(cl, slave, "MASTER_SSL=1, MASTER_SSL_CA='"+strings.ReplaceAll(stale, "'", "''")+"'"); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSStaleCAEmptied: %s", err)
		return false
	}
	if err := tlsWait(60*time.Second, func() error {
		if !tlsSecurityStateOpen(cl, "WARN0236", slave.URL) {
			return fmt.Errorf("WARN0236 not open on %s", slave.URL)
		}
		return nil
	}); err != nil {
		tlsChangeMasterRaw(cl, slave, "MASTER_SSL_CA=''")
		tlsLogf(cl, "FAIL ReplicationTLSStaleCAEmptied: the failed handshake is not reported: %s", err)
		return false
	}
	if _, err := slave.SetReplicationGTIDSlavePosFromServer(master); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSStaleCAEmptied: re-point: %s", err)
		return false
	}
	slave.StartSlave()
	if err := tlsSlavesReplicate(cl, 60*time.Second, func(ca string) bool { return ca == "" }); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSStaleCAEmptied: the CHANGE MASTER did not empty the stale CA: %s", err)
		return false
	}
	tlsLogf(cl, "PASS ReplicationTLSStaleCAEmptied")
	return true
}

// TestReplicationTLSVerifyOffWarns: a channel encrypted without CA and with certificate
// verification off opens WARN0235; turning verification back on resolves it.
func (regtest *RegTest) TestReplicationTLSVerifyOffWarns(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !tlsTopology(cl) || !tlsZeroConfig(cl) || tlsLiveVariable(cl.GetMaster(), "SSL_CERT") != "" {
		tlsLogf(cl, "Skipping ReplicationTLSVerifyOffWarns: needs a MariaDB 11.4+ master-slave cluster on auto-generated certificates")
		return true
	}
	slave := cl.GetSlaves()[0]
	if err := tlsChangeMasterRaw(cl, slave, "MASTER_SSL=1, MASTER_SSL_CA='', MASTER_SSL_VERIFY_SERVER_CERT=0"); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSVerifyOffWarns: %s", err)
		return false
	}
	defer tlsChangeMasterRaw(cl, slave, "MASTER_SSL_VERIFY_SERVER_CERT=1")
	if err := tlsWait(60*time.Second, func() error {
		if !tlsSecurityStateOpen(cl, "WARN0235", slave.URL) {
			return fmt.Errorf("WARN0235 not open on %s", slave.URL)
		}
		return nil
	}); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSVerifyOffWarns: %s", err)
		return false
	}
	if err := tlsChangeMasterRaw(cl, slave, "MASTER_SSL_VERIFY_SERVER_CERT=1"); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSVerifyOffWarns: %s", err)
		return false
	}
	if err := tlsWait(60*time.Second, func() error {
		if tlsSecurityStateOpen(cl, "WARN0235", slave.URL) {
			return fmt.Errorf("WARN0235 still open on %s", slave.URL)
		}
		return nil
	}); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSVerifyOffWarns: %s", err)
		return false
	}
	tlsLogf(cl, "PASS ReplicationTLSVerifyOffWarns")
	return true
}

// TestReplicationTLSSSLTagLifecycle: on a provisioned MariaDB 11.4+ cluster without the
// ssl tag, with replication-use-ssl on:
//  1. adding the ssl tag never costs the monitoring (no strictssl live, #1975; DSNs
//     rebuilt, #1974);
//  2. a rolling restart puts every server on the shipped certificates, the replicas then
//     verify the primary with the shipped CA;
//  3. dropping the tag never costs the monitoring, and the rolling restart that follows
//     puts the servers back on their auto-generated certificates (the delta does not
//     keep the tag, #1976), the replicas' CA emptied on the way.
func (regtest *RegTest) TestReplicationTLSSSLTagLifecycle(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	orch := cl.GetOrchestrator()
	if (orch != config.ConstOrchestratorOpenSVC && orch != config.ConstOrchestratorKubernetes) || !tlsTopology(cl) || !tlsZeroConfig(cl) || cl.Configurator.HaveDBTag("ssl") {
		tlsLogf(cl, "Skipping ReplicationTLSSSLTagLifecycle: needs a provisioned MariaDB 11.4+ master-slave cluster without the ssl tag")
		return true
	}
	prev := cl.Conf.ReplicationSSL
	cl.Conf.ReplicationSSL = true
	defer func() { cl.Conf.ReplicationSSL = prev }()
	tagged := false
	defer func() {
		if tagged {
			cl.DropDBTag("ssl", false)
			tlsLogf(cl, "ReplicationTLSSSLTagLifecycle: ssl tag dropped on the way out, a rolling restart puts the servers back on their own certificates")
		}
	}()

	cl.AddDBTag("ssl", false)
	tagged = true
	if err := tlsMonitoringKept(cl, 20*time.Second); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSSSLTagLifecycle: adding the ssl tag cost the monitoring: %s", err)
		return false
	}
	if !rollingHealthCheck(cl, "RollingRestart(ssl tag)", cl.RollingRestart) {
		return false
	}
	for _, s := range cl.Servers {
		if cert := tlsLiveVariable(s, "SSL_CERT"); cert == "" {
			tlsLogf(cl, "FAIL ReplicationTLSSSLTagLifecycle: %s not on the shipped certificate after the rolling restart", s.URL)
			return false
		}
	}
	for i := 0; i < 2; i++ {
		cl.SwitchoverWaitTest()
	}
	if err := tlsSlavesReplicate(cl, 60*time.Second, func(ca string) bool { return ca == cluster.ReplicationSSLCaProvisioned }); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSSSLTagLifecycle: with the ssl tag: %s", err)
		return false
	}

	cl.DropDBTag("ssl", false)
	tagged = false
	if err := tlsMonitoringKept(cl, 20*time.Second); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSSSLTagLifecycle: dropping the ssl tag cost the monitoring: %s", err)
		return false
	}
	if !rollingHealthCheck(cl, "RollingRestart(ssl tag dropped)", cl.RollingRestart) {
		return false
	}
	for _, s := range cl.Servers {
		if cert := tlsLiveVariable(s, "SSL_CERT"); cert != "" {
			tlsLogf(cl, "FAIL ReplicationTLSSSLTagLifecycle: %s still on %s after the tag was dropped (delta kept it?)", s.URL, cert)
			return false
		}
	}
	if err := tlsSlavesReplicate(cl, 60*time.Second, func(ca string) bool { return ca == "" }); err != nil {
		tlsLogf(cl, "FAIL ReplicationTLSSSLTagLifecycle: after the tag was dropped: %s", err)
		return false
	}
	tlsLogf(cl, "PASS ReplicationTLSSSLTagLifecycle")
	return true
}

// TestRollingUpgradeTransparentSSL: a provisioned MariaDB cluster on a release without
// zero-configuration TLS (before 11.4) is upgraded on the 11.8 line. The rolling upgrade
// must keep the cluster healthy through the mixed versions (an 11.4+ replica of a
// primary without TLS gets MASTER_SSL=0, MDEV-35584), and once every node runs 11.8
// with replication-use-ssl switched on, two switchovers leave every replica encrypted
// on the auto-generated certificates, without CA, replicating, and no TLS warning open.
func (regtest *RegTest) TestRollingUpgradeTransparentSSL(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	orch := cl.GetOrchestrator()
	if orch != config.ConstOrchestratorOpenSVC && orch != config.ConstOrchestratorKubernetes || !tlsTopology(cl) {
		tlsLogf(cl, "Skipping RollingUpgradeTransparentSSL: needs a provisioned master-slave cluster")
		return true
	}
	for _, s := range cl.Servers {
		if s.DBVersion == nil || !s.DBVersion.IsMariaDB() || s.DBVersion.GreaterEqual("11.4") {
			tlsLogf(cl, "Skipping RollingUpgradeTransparentSSL: every server must run MariaDB before 11.4 (%s)", s.URL)
			return true
		}
	}
	prev := cl.Conf.ReplicationSSL
	cl.Conf.ReplicationSSL = false
	defer func() { cl.Conf.ReplicationSSL = prev }()

	plan, err := cl.PrepareRollingUpgrade(releases.TargetVersion, tlsReplicationUpgradeLine)
	if err != nil {
		tlsLogf(cl, "FAIL RollingUpgradeTransparentSSL: plan refused: %s", err)
		return false
	}
	_, release := releases.SplitImage(plan.TargetImage)
	tlsLogf(cl, "RollingUpgradeTransparentSSL plan: %s -> %s (%s)", plan.CurrentImage, plan.TargetImage, plan.Mechanic)
	if !rollingHealthCheckVersions(cl, "RollingUpgradeTransparentSSL", func() error { return cl.RunRollingUpgrade(plan) }, release) {
		return false
	}
	if err := tlsSlavesReplicate(cl, 60*time.Second, nil); err != nil {
		tlsLogf(cl, "FAIL RollingUpgradeTransparentSSL: after the upgrade: %s", err)
		return false
	}

	cl.Conf.ReplicationSSL = true
	for i := 0; i < 2; i++ {
		cl.SwitchoverWaitTest()
	}
	if err := tlsSlavesReplicate(cl, 60*time.Second, func(ca string) bool { return ca == "" }); err != nil {
		tlsLogf(cl, "FAIL RollingUpgradeTransparentSSL: with replication-use-ssl on %s: %s", release, err)
		return false
	}
	for _, s := range cl.GetSlaves() {
		if st, _ := tlsChannel(cl, s); st.MasterSSLAllowed.String != "Yes" {
			tlsLogf(cl, "FAIL RollingUpgradeTransparentSSL: %s not encrypted with replication-use-ssl on", s.URL)
			return false
		}
	}
	time.Sleep(10 * time.Second)
	if tlsSecurityStateOpen(cl, "WARN0236", "") {
		tlsLogf(cl, "FAIL RollingUpgradeTransparentSSL: a TLS handshake fails after the upgrade")
		return false
	}
	if tlsSecurityStateOpen(cl, "WARN0235", "") {
		// A channel created on the old release keeps MASTER_SSL_VERIFY_SERVER_CERT=No:
		// reported, not a failure (verification is the operator's to turn on).
		tlsLogf(cl, "RollingUpgradeTransparentSSL: WARN0235 open, channels created before 11.4 keep certificate verification off")
	}
	tlsLogf(cl, "PASS RollingUpgradeTransparentSSL: %s -> %s", plan.CurrentImage, plan.TargetImage)
	return true
}
