package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/viper"
)

// TestSetDefaultFlags_InitsCheckSumConfig verifies the source fix: CheckSumConfig is
// allocated at the earliest startup init (before InitConfig and any save), so no
// config-save path can hit a nil map.
func TestSetDefaultFlags_InitsCheckSumConfig(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}

	repman.SetDefaultFlags(viper.New())

	if repman.CheckSumConfig == nil {
		t.Fatal("SetDefaultFlags must allocate CheckSumConfig")
	}
}

// TestSaveDynamic_NilSafeAfterSetDefaultFlags exercises a direct writer. SaveDynamic
// writes CheckSumConfig itself and is not individually guarded — the SetDefaultFlags
// init is what keeps it (and ReloadTerms, Overwrite) nil-safe. Pins that guarantee.
func TestSaveDynamic_NilSafeAfterSetDefaultFlags(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}

	repman.SetDefaultFlags(viper.New())

	if _, err := repman.SaveDynamic(); err != nil {
		t.Fatalf("SaveDynamic returned an error: %v", err)
	}
	if repman.CheckSumConfig == nil {
		t.Fatal("CheckSumConfig must be set after SetDefaultFlags + SaveDynamic")
	}
}

// TestSaveGlobalConfigs_NilCheckSumConfig pins the defensive guard in SaveGlobalConfigs:
// a bare ReplicationManager (no SetDefaultFlags) with a nil CheckSumConfig must still
// not panic when a save is triggered — the release-blocking boot regression.
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
