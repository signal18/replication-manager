package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestSaveGlobalConfigs_NilCheckSumConfig pins the boot-crash regression fixed by
// the nil-map guard in SaveGlobalConfigs. A config save can be triggered before
// CheckSumConfig is allocated — syncSubscriptionPlanFromCRM persists the plan
// synchronously at the tail of InitConfig, well before the map is created later in
// startup. Writing to a nil map panics; the save path must instead find it ready.
func TestSaveGlobalConfigs_NilCheckSumConfig(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{
			ConfRewrite: true,
			WorkingDir:  t.TempDir(),
		},
	}

	// CheckSumConfig left nil on purpose, reproducing the early-startup state.
	if err := repman.SaveGlobalConfigs(); err != nil {
		t.Fatalf("SaveGlobalConfigs returned an error with nil CheckSumConfig: %v", err)
	}

	if repman.CheckSumConfig == nil {
		t.Fatal("SaveGlobalConfigs must lazily initialize CheckSumConfig when nil")
	}
}
