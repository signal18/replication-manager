package cluster

import (
	"testing"
)

// ---- SecurityScore.ApplyCheck -----------------------------------------------

func TestSecurityScore_ApplyCheck_KnownTags(t *testing.T) {
	cases := []struct {
		tag  string
		set  func(*SecurityScore) bool
	}{
		{"HasSSL", func(s *SecurityScore) bool { return s.HasSSL }},
		{"HasZeroSSL", func(s *SecurityScore) bool { return s.HasZeroSSL }},
		{"HasTableEncryption", func(s *SecurityScore) bool { return s.HasTableEncryption }},
		{"HasBinlogEncryption", func(s *SecurityScore) bool { return s.HasBinlogEncryption }},
		{"HasTmpEncryption", func(s *SecurityScore) bool { return s.HasTmpEncryption }},
		{"HasBackupEncryption", func(s *SecurityScore) bool { return s.HasBackupEncryption }},
		{"HasAuditPlugins", func(s *SecurityScore) bool { return s.HasAuditPlugins }},
		{"NoEmptyPassword", func(s *SecurityScore) bool { return s.NoEmptyPassword }},
		{"HasPrepareStatement", func(s *SecurityScore) bool { return s.HasPrepareStatement }},
		{"HasStrongPwd", func(s *SecurityScore) bool { return s.HasStrongPwd }},
		{"HasProxies", func(s *SecurityScore) bool { return s.HasProxies }},
		{"HasParsecPlugins", func(s *SecurityScore) bool { return s.HasParsecPlugins }},
		{"HasPasswordRotation", func(s *SecurityScore) bool { return s.HasPasswordRotation }},
		{"NoWeakAuthPlugin", func(s *SecurityScore) bool { return s.NoWeakAuthPlugin }},
		{"NoHostnameGrants", func(s *SecurityScore) bool { return s.NoHostnameGrants }},
		{"NoClearPwdConfigs", func(s *SecurityScore) bool { return s.NoClearPwdConfigs }},
		{"NoClearPwdHistory", func(s *SecurityScore) bool { return s.NoClearPwdHistory }},
		{"NoClearPwdBinlogs", func(s *SecurityScore) bool { return s.NoClearPwdBinlogs }},
		{"HasLastLTS", func(s *SecurityScore) bool { return s.HasLastLTS }},
	}

	for _, tc := range cases {
		var s SecurityScore
		s.ApplyCheck(tc.tag, true)
		if !tc.set(&s) {
			t.Errorf("ApplyCheck(%q, true): field not set", tc.tag)
		}
		s.ApplyCheck(tc.tag, false)
		if tc.set(&s) {
			t.Errorf("ApplyCheck(%q, false): field not cleared", tc.tag)
		}
	}
}

func TestSecurityScore_ApplyCheck_UnknownTagIgnored(t *testing.T) {
	var s SecurityScore
	s.ApplyCheck("UnknownFutureTag", true) // must not panic
}

// TestSecurityScore_ApplyCheck_ANDSemantics verifies that when multiple servers
// report the same tag in one tick, the cluster result is the AND — a replica
// that passes cannot mask a master that fails (the flapping bug scenario).
func TestSecurityScore_ApplyCheck_ANDSemantics(t *testing.T) {
	// Scenario A: master fails, replica passes → cluster must FAIL
	var s SecurityScore
	s.ApplyCheck("HasAuditPlugins", false) // master: no audit
	s.ApplyCheck("HasAuditPlugins", true)  // replica: has audit
	if s.HasAuditPlugins {
		t.Error("cluster should FAIL when master has no audit even if replica does")
	}

	// Scenario B: master passes, replica fails → cluster must FAIL
	s = SecurityScore{}
	s.ApplyCheck("HasAuditPlugins", true)  // master: has audit
	s.ApplyCheck("HasAuditPlugins", false) // replica: no audit
	if s.HasAuditPlugins {
		t.Error("cluster should FAIL when replica has no audit even if master does")
	}

	// Scenario C: both pass → cluster must PASS
	s = SecurityScore{}
	s.ApplyCheck("HasAuditPlugins", true)
	s.ApplyCheck("HasAuditPlugins", true)
	if !s.HasAuditPlugins {
		t.Error("cluster should PASS when all servers have audit")
	}
}

// ---- SecurityScore.Compute --------------------------------------------------

func TestSecurityScore_Compute_AllPass(t *testing.T) {
	s := SecurityScore{
		HasSSL: true, HasZeroSSL: true, HasTableEncryption: true,
		HasBinlogEncryption: true, HasTmpEncryption: true, HasBackupEncryption: true,
		HasAuditPlugins: true, NoEmptyPassword: true, HasPrepareStatement: true,
		HasStrongPwd: true, HasProxies: true, HasParsecPlugins: true,
		HasPasswordRotation: true, NoWeakAuthPlugin: true, NoHostnameGrants: true,
		NoClearPwdConfigs: true, NoClearPwdHistory: true, NoClearPwdBinlogs: true,
		HasLastLTS: true,
	}
	s.Compute()
	if s.Score != 100 {
		t.Errorf("all pass: Score = %d, want 100", s.Score)
	}
	if s.Grade != "A" {
		t.Errorf("all pass: Grade = %q, want A", s.Grade)
	}
}

func TestSecurityScore_Compute_AllFail(t *testing.T) {
	var s SecurityScore
	s.Compute()
	if s.Score != 0 {
		t.Errorf("all fail: Score = %d, want 0", s.Score)
	}
	if s.Grade != "F" {
		t.Errorf("all fail: Grade = %q, want F", s.Grade)
	}
}

func TestSecurityScore_Compute_GradeBoundaries(t *testing.T) {
	// With 19 checks, score = passed*100/19.
	// Exact scores per passed count:
	//  19→100, 18→94, 17→89, 15→78, 14→73, 12→63, 11→57, 8→42, 7→36, 0→0
	cases := []struct {
		passed    int
		wantGrade string
	}{
		{19, "A"}, // 100 ≥ 90
		{18, "A"}, // 94  ≥ 90
		{17, "B"}, // 89  ≥ 75
		{15, "B"}, // 78  ≥ 75
		{14, "C"}, // 73  ≥ 60
		{12, "C"}, // 63  ≥ 60
		{11, "D"}, // 57  ≥ 40
		{8, "D"},  // 42  ≥ 40
		{7, "F"},  // 36  < 40
		{0, "F"},  // 0
	}

	for _, tc := range cases {
		s := makeScoreWithN(tc.passed, 19)
		s.Compute()
		if s.Grade != tc.wantGrade {
			t.Errorf("passed=%d/19 score=%d: Grade = %q, want %q",
				tc.passed, s.Score, s.Grade, tc.wantGrade)
		}
	}
}

// makeScoreWithN returns a SecurityScore with exactly n checks set to true
// (in field order matching Compute's checks slice).
func makeScoreWithN(n, _ int) SecurityScore {
	fields := []*bool{}
	s := SecurityScore{}
	fields = append(fields,
		&s.HasSSL, &s.HasZeroSSL, &s.HasTableEncryption, &s.HasBinlogEncryption,
		&s.HasTmpEncryption, &s.HasBackupEncryption, &s.HasAuditPlugins,
		&s.NoEmptyPassword, &s.HasPrepareStatement, &s.HasStrongPwd, &s.HasProxies,
		&s.HasParsecPlugins, &s.HasPasswordRotation, &s.NoWeakAuthPlugin,
		&s.NoHostnameGrants, &s.NoClearPwdConfigs, &s.NoClearPwdHistory,
		&s.NoClearPwdBinlogs, &s.HasLastLTS,
	)
	for i := 0; i < n && i < len(fields); i++ {
		*fields[i] = true
	}
	return s
}

// ---- binlogSyncerTLSConfig --------------------------------------------------

func TestBinlogSyncerTLSConfig_NoTLS(t *testing.T) {
	srv := &ServerMonitor{ClusterGroup: &Cluster{}}
	if cfg := srv.binlogSyncerTLSConfig(); cfg != nil {
		t.Errorf("expected nil TLS config, got %+v", cfg)
	}
}

func TestBinlogSyncerTLSConfig_ForceSkipVerify(t *testing.T) {
	srv := &ServerMonitor{
		ClusterGroup:       &Cluster{},
		ForceTLSSkipVerify: true,
	}
	cfg := srv.binlogSyncerTLSConfig()
	if cfg == nil {
		t.Fatal("expected non-nil TLS config for ForceTLSSkipVerify")
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true")
	}
}

func TestBinlogSyncerTLSConfig_HaveDBTLSCert(t *testing.T) {
	// tlsconf is unexported and nil in test — the important assertion is that the
	// ForceTLSSkipVerify branch is NOT taken when HaveDBTLSCert=true.
	cl := &Cluster{HaveDBTLSCert: true}
	srv := &ServerMonitor{ClusterGroup: cl}
	cfg := srv.binlogSyncerTLSConfig()
	// When HaveDBTLSCert=true, tlsconf is nil (not set in test), so result is nil.
	// The important assertion is that ForceTLSSkipVerify branch is NOT taken.
	if cfg != nil && cfg.InsecureSkipVerify {
		t.Error("HaveDBTLSCert path must not set InsecureSkipVerify=true")
	}
}
