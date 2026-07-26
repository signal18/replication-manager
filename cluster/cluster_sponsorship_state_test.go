package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newSponsorshipTestCluster(t *testing.T) *Cluster {
	t.Helper()
	return &Cluster{
		Name:      "test",
		WorkingDir: t.TempDir(),
		Conf:      &config.Config{},
	}
}

func TestSponsorshipState_WriteReadRoundTrip(t *testing.T) {
	cl := newSponsorshipTestCluster(t)

	if err := cl.SetSponsorshipActive("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipActive: %v", err)
	}

	st, err := cl.LoadSponsorshipState()
	if err != nil {
		t.Fatalf("LoadSponsorshipState: %v", err)
	}

	if st.Status != SponsorshipStatusActive {
		t.Errorf("Status = %q, want %q", st.Status, SponsorshipStatusActive)
	}
	if st.ClusterRef != "test" {
		t.Errorf("ClusterRef = %q, want %q", st.ClusterRef, "test")
	}
	if st.Audit.SubjectUsername != "alice" || st.Audit.ActorUsername != "bob" {
		t.Errorf("Audit = %+v, want subject=alice actor=bob", st.Audit)
	}
	if st.LastWorkflowEvent.EventType != "sponsorship_approved" {
		t.Errorf("LastWorkflowEvent.EventType = %q, want sponsorship_approved", st.LastWorkflowEvent.EventType)
	}
	if st.LastWorkflowEvent.EventKey == "" {
		t.Error("LastWorkflowEvent.EventKey is empty")
	}
	if st.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}

func TestLoadSponsorshipState_MissingFileIsNotAnError(t *testing.T) {
	cl := newSponsorshipTestCluster(t)

	st, err := cl.LoadSponsorshipState()
	if err != nil {
		t.Fatalf("LoadSponsorshipState returned error for missing file: %v", err)
	}
	if st.Status != SponsorshipStatusNone {
		t.Errorf("Status = %q, want %q", st.Status, SponsorshipStatusNone)
	}
}

func TestRestoreSponsorshipState_FirstRun(t *testing.T) {
	cl := newSponsorshipTestCluster(t)

	if err := cl.RestoreSponsorshipState(); err != nil {
		t.Fatalf("RestoreSponsorshipState: %v", err)
	}
	if got := cl.GetSponsorshipState().Status; got != SponsorshipStatusNone {
		t.Errorf("Status = %q, want %q", got, SponsorshipStatusNone)
	}
	if _, err := os.Stat(sponsorshipStatePath(cl.WorkingDir)); !os.IsNotExist(err) {
		t.Errorf("expected no sponsorship-state.json to be created by a read-only restore, stat err = %v", err)
	}
}

func TestRestoreSponsorshipState_ExistingFile(t *testing.T) {
	workingDir := t.TempDir()

	writer := &Cluster{Name: "test", WorkingDir: workingDir, Conf: &config.Config{}}
	if err := writer.SetSponsorshipActive("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipActive: %v", err)
	}

	reader := &Cluster{Name: "test", WorkingDir: workingDir, Conf: &config.Config{}}
	if err := reader.RestoreSponsorshipState(); err != nil {
		t.Fatalf("RestoreSponsorshipState: %v", err)
	}

	got := reader.GetSponsorshipState()
	want := writer.GetSponsorshipState()
	if got.Status != want.Status || got.Audit.SubjectUsername != want.Audit.SubjectUsername {
		t.Errorf("restored state = %+v, want %+v", got, want)
	}
}

func TestSponsorshipTransitions_Ordering(t *testing.T) {
	cl := newSponsorshipTestCluster(t)

	if err := cl.SetSponsorshipRequested("alice", "alice"); err != nil {
		t.Fatalf("SetSponsorshipRequested: %v", err)
	}
	requested := cl.GetSponsorshipState()
	if requested.Status != SponsorshipStatusRequested {
		t.Fatalf("Status after request = %q, want %q", requested.Status, SponsorshipStatusRequested)
	}

	if err := cl.SetSponsorshipActive("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipActive: %v", err)
	}
	active := cl.GetSponsorshipState()
	if active.Status != SponsorshipStatusActive {
		t.Fatalf("Status after accept = %q, want %q", active.Status, SponsorshipStatusActive)
	}
	if !active.UpdatedAt.After(requested.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: requested=%v active=%v", requested.UpdatedAt, active.UpdatedAt)
	}

	if err := cl.SetSponsorshipEnded("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipEnded: %v", err)
	}
	ended := cl.GetSponsorshipState()
	if ended.Status != SponsorshipStatusEnded {
		t.Fatalf("Status after end = %q, want %q", ended.Status, SponsorshipStatusEnded)
	}
	if !ended.UpdatedAt.After(active.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: active=%v ended=%v", active.UpdatedAt, ended.UpdatedAt)
	}
}

func TestSponsorshipTransitions_RejectedIsTerminal(t *testing.T) {
	cl := newSponsorshipTestCluster(t)

	if err := cl.SetSponsorshipRequested("alice", "alice"); err != nil {
		t.Fatalf("SetSponsorshipRequested: %v", err)
	}
	if err := cl.SetSponsorshipRejected("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipRejected: %v", err)
	}

	st := cl.GetSponsorshipState()
	if st.Status != SponsorshipStatusRejected {
		t.Errorf("Status = %q, want %q", st.Status, SponsorshipStatusRejected)
	}
}

func TestSponsorshipState_AtomicWriteNoTempFileLeftover(t *testing.T) {
	cl := newSponsorshipTestCluster(t)

	if err := cl.SetSponsorshipActive("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipActive: %v", err)
	}

	entries, err := os.ReadDir(cl.WorkingDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file found: %s", e.Name())
		}
	}
	if _, err := os.Stat(sponsorshipStatePath(cl.WorkingDir)); err != nil {
		t.Errorf("sponsorship-state.json missing: %v", err)
	}
}

func TestSponsorshipState_WriteFailureDoesNotUpdateInMemoryState(t *testing.T) {
	dir := t.TempDir()
	// Make sponsorship-state.json's parent path unwritable by pre-creating a
	// regular file where the state directory needs to be, so MkdirAll fails.
	blockedDir := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blockedDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cl := &Cluster{Name: "test", WorkingDir: blockedDir, Conf: &config.Config{}}

	before := cl.GetSponsorshipState()
	if err := cl.SetSponsorshipActive("alice", "bob"); err == nil {
		t.Fatal("expected SetSponsorshipActive to fail when WorkingDir is unwritable")
	}
	after := cl.GetSponsorshipState()

	if after.Status != before.Status {
		t.Errorf("in-memory state changed after a failed write: before=%+v after=%+v", before, after)
	}
}

func TestApplySponsorshipMirror_CopiesSafeFieldsOnly(t *testing.T) {
	cl := newSponsorshipTestCluster(t)
	cl.Conf.Cloud18MarketplacePricingMode = "global-unit-pricing"

	if err := cl.SetSponsorshipActive("alice", "bob"); err != nil {
		t.Fatalf("SetSponsorshipActive: %v", err)
	}

	var clsave ClusterState
	applySponsorshipMirror(&clsave, cl.GetSponsorshipState(), cl.Conf.Cloud18MarketplacePricingMode)

	if clsave.SponsorshipStatus != SponsorshipStatusActive {
		t.Errorf("SponsorshipStatus = %q, want %q", clsave.SponsorshipStatus, SponsorshipStatusActive)
	}
	if clsave.SponsorshipClusterRef != "test" {
		t.Errorf("SponsorshipClusterRef = %q, want %q", clsave.SponsorshipClusterRef, "test")
	}
	if clsave.SponsorshipPricingMode != "global-unit-pricing" {
		t.Errorf("SponsorshipPricingMode = %q, want %q", clsave.SponsorshipPricingMode, "global-unit-pricing")
	}
	if clsave.SponsorshipLastEventType != "sponsorship_approved" {
		t.Errorf("SponsorshipLastEventType = %q, want sponsorship_approved", clsave.SponsorshipLastEventType)
	}

	data, err := json.Marshal(clsave)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "billingOwnerRef") ||
		strings.Contains(string(data), "subjectUsername") ||
		strings.Contains(string(data), "actorUsername") ||
		strings.Contains(string(data), "eventKey") {
		t.Errorf("ClusterState JSON leaked non-mirrored sponsorship fields: %s", string(data))
	}
}
