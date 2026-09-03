package config

import "testing"

func mkVarState(name, cfg, runtime, preserved, source string) *VariableState {
	v := NewVariableState(name)
	if cfg != "" {
		v.SetConfigValue(cfg)
	}
	if runtime != "" {
		v.SetRuntimeValue(runtime)
	}
	if preserved != "" {
		v.SetPreservedValue(preserved)
	}
	v.PreservedSource = source
	return v
}

func TestDropReconciledAgreed(t *testing.T) {
	m := NewVariablesMap()
	// agreed (no source) + reconciled (runtime == config) -> dropped
	m.Store("agreed_reconciled", mkVarState("agreed_reconciled", "4096", "4096", "4096", ""))
	// agreed but not yet reconciled (runtime != config) -> kept
	m.Store("agreed_pending", mkVarState("agreed_pending", "4096", "16384", "16384", ""))
	// operator-forced preserve + reconciled -> MUST never be dropped
	m.Store("forced", mkVarState("forced", "4096", "4096", "4096", "server-specific"))
	// cluster-level forced preserve + reconciled -> MUST never be dropped
	m.Store("forced_cluster", mkVarState("forced_cluster", "4096", "4096", "4096", "cluster-level"))
	// nothing preserved -> untouched
	m.Store("plain", mkVarState("plain", "4096", "4096", "", ""))

	dropped := m.DropReconciledAgreed()

	if len(dropped) != 1 || dropped[0] != "agreed_reconciled" {
		t.Fatalf("expected only [agreed_reconciled] dropped, got %v", dropped)
	}

	assertPreserved := func(key string, wantNil bool) {
		v, ok := m.Load(key)
		if !ok {
			t.Fatalf("%s: missing", key)
		}
		got := v.(*VariableState).Preserved
		if wantNil && got != nil {
			t.Errorf("%s: Preserved should be nil after drop", key)
		}
		if !wantNil && got == nil {
			t.Errorf("%s: Preserved must be kept", key)
		}
	}
	assertPreserved("agreed_reconciled", true) // dropped
	assertPreserved("agreed_pending", false)   // kept: not reconciled
	assertPreserved("forced", false)           // kept: server-specific forced
	assertPreserved("forced_cluster", false)   // kept: cluster-level forced
}

func mkVarDeployed(name, cfg, deployed, preserved string, dropped bool) *VariableState {
	v := NewVariableState(name)
	if cfg != "" {
		v.SetConfigValue(cfg)
	}
	if deployed != "" {
		v.SetDeployedValue(deployed)
	}
	if preserved != "" {
		v.SetPreservedValue(preserved)
	}
	v.Dropped = dropped
	return v
}

func TestAutoAgreeValueDeltas(t *testing.T) {
	m := NewVariablesMap()
	// value-differs (config known, deployed != config, not preserved/dropped) -> agreed to config
	m.Store("value_differs", mkVarDeployed("value_differs", "4096", "16384", "", false))
	// no-config (Config nil) -> NOT agreed (deprecated/unknown, manual review)
	m.Store("no_config", mkVarDeployed("no_config", "", "16384", "", false))
	// already preserved -> untouched
	m.Store("already", mkVarDeployed("already", "4096", "16384", "16384", false))
	// dropped -> NOT agreed
	m.Store("dropped", mkVarDeployed("dropped", "4096", "16384", "", true))
	// deployed == config (no diff) -> NOT agreed
	m.Store("equal", mkVarDeployed("equal", "4096", "4096", "", false))

	agreed := m.AutoAgreeValueDeltas()

	if len(agreed) != 1 || agreed[0] != "value_differs" {
		t.Fatalf("expected only [value_differs] agreed, got %v", agreed)
	}
	// value_differs now has Preserved == Config (agreed)
	v, _ := m.Load("value_differs")
	vs := v.(*VariableState)
	if vs.Preserved == nil || vs.Preserved.String() != "4096" {
		t.Errorf("value_differs: expected Preserved=4096 (config), got %v", vs.Preserved)
	}
	// no_config, dropped, equal untouched (no Preserved set by us)
	for _, k := range []string{"no_config", "dropped", "equal"} {
		v, _ := m.Load(k)
		if v.(*VariableState).Preserved != nil {
			t.Errorf("%s: should not have been agreed", k)
		}
	}
}

// TestUnknownVariableSafety locks in that a variable not recognized by the DB
// (PreservedSource == "unknown-variable", Preserved set by the unknown detector)
// is never auto-agreed (would be deployed -> crash) and never auto-dropped
// (must stay in 03_agreed for manual review).
func TestUnknownVariableSafety(t *testing.T) {
	// unknown: Config set, Preserved == Config, source "unknown-variable", no runtime (DB doesn't know it)
	unknown := mkVarState("some_unknown_var", "1", "", "1", "unknown-variable")

	m1 := NewVariablesMap()
	m1.Store("some_unknown_var", unknown)
	if agreed := m1.AutoAgreeValueDeltas(); len(agreed) != 0 {
		t.Errorf("unknown must never be auto-agreed, got %v", agreed)
	}
	if v, _ := m1.Load("some_unknown_var"); v.(*VariableState).Preserved == nil {
		t.Errorf("unknown Preserved must be kept (routed to agreed for review)")
	}

	// Even if runtime somehow equals config, DropReconciledAgreed must skip it (source != "")
	unknown2 := mkVarState("some_unknown_var", "1", "1", "1", "unknown-variable")
	m2 := NewVariablesMap()
	m2.Store("some_unknown_var", unknown2)
	if dropped := m2.DropReconciledAgreed(); len(dropped) != 0 {
		t.Errorf("unknown must never be auto-dropped, got %v", dropped)
	}
	if v, _ := m2.Load("some_unknown_var"); v.(*VariableState).Preserved == nil {
		t.Errorf("unknown Preserved must survive DropReconciledAgreed")
	}
}
