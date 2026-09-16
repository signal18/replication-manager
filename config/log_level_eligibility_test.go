// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// ALERTOK is the recovery-alert counterpart to ALERT (see cluster/app.go and
// cluster/cluster_log.go's ALERTOK case). Unlike ALERT -- mapped to
// NumLvlError above -- ALERTOK was previously unmapped, so it always fell
// through to `return false` regardless of LogLevel, silently dropping every
// recovery notification unless forcingLog was set.
func TestIsEligibleForPrinting_AlertOkUsesInfoThreshold(t *testing.T) {
	conf := &Config{LogLevel: NumLvlInfo}
	if !conf.IsEligibleForPrinting(ConstLogModGeneral, "ALERTOK") {
		t.Fatalf("expected ALERTOK eligible at LogLevel=%d (informational)", NumLvlInfo)
	}

	conf = &Config{LogLevel: NumLvlDebug}
	if !conf.IsEligibleForPrinting(ConstLogModGeneral, "ALERTOK") {
		t.Fatalf("expected ALERTOK eligible at LogLevel=%d (debug, above informational)", NumLvlDebug)
	}
}

func TestIsEligibleForPrinting_AlertOkFilteredBelowInfoThreshold(t *testing.T) {
	conf := &Config{LogLevel: NumLvlWarn}
	if conf.IsEligibleForPrinting(ConstLogModGeneral, "ALERTOK") {
		t.Fatalf("expected ALERTOK filtered at LogLevel=%d (below informational)", NumLvlWarn)
	}

	conf = &Config{LogLevel: NumLvlError}
	if conf.IsEligibleForPrinting(ConstLogModGeneral, "ALERTOK") {
		t.Fatalf("expected ALERTOK filtered at LogLevel=%d (below informational)", NumLvlError)
	}
}

// ALERT must remain unaffected by the ALERTOK fix: it stays mapped to the
// error threshold, so it is eligible at any LogLevel >= NumLvlError.
func TestIsEligibleForPrinting_AlertUnaffectedByAlertOkFix(t *testing.T) {
	conf := &Config{LogLevel: NumLvlError}
	if !conf.IsEligibleForPrinting(ConstLogModGeneral, "ALERT") {
		t.Fatalf("expected ALERT eligible at LogLevel=%d (error)", NumLvlError)
	}
}
