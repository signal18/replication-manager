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
