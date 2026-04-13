package backupmgr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BuildDirectoryRestorePlan tests
// ---------------------------------------------------------------------------

func TestBuildDirectoryRestorePlan_GroupsAndSorts(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/db1.tbl1.0002.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "db1.tbl1"},
			{Path: "data/db1.tbl1.0001.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "db1.tbl1"},
			{Path: "data/db1.tbl2.0001.sql", Class: StreamEntryClassData, OrderHint: 3, GroupHint: "db1.tbl2"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	if len(plan.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(plan.Groups))
	}

	// db1.tbl1 group should appear first (lower min OrderHint = 1 vs 3)
	g0 := plan.Groups[0]
	if g0.GroupHint != "db1.tbl1" {
		t.Fatalf("expected group 0 = db1.tbl1, got %q", g0.GroupHint)
	}
	if len(g0.Entries) != 2 {
		t.Fatalf("expected 2 entries in db1.tbl1 group, got %d", len(g0.Entries))
	}
	// Within group: sorted by OrderHint
	if g0.Entries[0].OrderHint != 1 || g0.Entries[1].OrderHint != 2 {
		t.Fatalf("entries not sorted by OrderHint: %v %v", g0.Entries[0].OrderHint, g0.Entries[1].OrderHint)
	}
}

func TestBuildDirectoryRestorePlan_SchemaBeforeData(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/db1.tbl1.0001.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "db1.tbl1"},
			{Path: "schema/db1.sql", Class: StreamEntryClassSchema, OrderHint: 1, GroupHint: "db1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	if len(plan.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(plan.Groups))
	}

	if plan.Groups[0].Entries[0].Class != StreamEntryClassSchema {
		t.Fatalf("expected schema group first, got class %d", plan.Groups[0].Entries[0].Class)
	}
	if plan.Groups[1].Entries[0].Class != StreamEntryClassData {
		t.Fatalf("expected data group second, got class %d", plan.Groups[1].Entries[0].Class)
	}
}

func TestBuildDirectoryRestorePlan_ClassOrdering(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "meta/checksum.json", Class: StreamEntryClassMeta, OrderHint: 10, GroupHint: "meta"},
			{Path: "data/db1.tbl1.0001.sql", Class: StreamEntryClassData, OrderHint: 5, GroupHint: "db1.tbl1"},
			{Path: "schema/db1.sql", Class: StreamEntryClassSchema, OrderHint: 1, GroupHint: "db1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	if len(plan.Groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(plan.Groups))
	}

	wantOrder := []StreamEntryClass{StreamEntryClassSchema, StreamEntryClassData, StreamEntryClassMeta}
	for i, want := range wantOrder {
		got := plan.Groups[i].Entries[0].Class
		if got != want {
			t.Fatalf("group[%d] expected class %d, got %d", i, want, got)
		}
	}
}

func TestBuildDirectoryRestorePlan_RejectsNonDirectoryMode(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeSingleFile,
		Entries: []StreamEntryIndex{
			{Path: "backup.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "full"},
		},
	}

	_, err := BuildDirectoryRestorePlan(preflight)
	if err == nil {
		t.Fatalf("expected error for single-file mode, got nil")
	}
}

func TestBuildDirectoryRestorePlan_RejectsNilPreflight(t *testing.T) {
	t.Parallel()

	_, err := BuildDirectoryRestorePlan(nil)
	if err == nil {
		t.Fatalf("expected error for nil preflight, got nil")
	}
}

func TestBuildDirectoryRestorePlan_RejectsEmptyEntries(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode:    StreamModeDirectory,
		Entries: nil,
	}

	_, err := BuildDirectoryRestorePlan(preflight)
	if err == nil {
		t.Fatalf("expected error for empty entries, got nil")
	}
}

func TestBuildDirectoryRestorePlan_SingleEntryGroup(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "schema/db1.sql", Class: StreamEntryClassSchema, OrderHint: 1, GroupHint: "db1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	if len(plan.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(plan.Groups))
	}
	if len(plan.Groups[0].Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(plan.Groups[0].Entries))
	}
}

// ---------------------------------------------------------------------------
// ExecuteDirectoryRestore — policy: sequential
// ---------------------------------------------------------------------------

func TestExecuteDirectoryRestore_Sequential_OrderPreserved(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "schema/db1.sql", Class: StreamEntryClassSchema, OrderHint: 1, GroupHint: "db1"},
			{Path: "data/db1.tbl1.0001.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "db1.tbl1"},
			{Path: "data/db1.tbl1.0002.sql", Class: StreamEntryClassData, OrderHint: 3, GroupHint: "db1.tbl1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	var mu sync.Mutex
	var order []string

	fn := func(_ context.Context, entry StreamEntryIndex) error {
		mu.Lock()
		order = append(order, entry.Path)
		mu.Unlock()
		return nil
	}

	if err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicySequential, fn); err != nil {
		t.Fatalf("ExecuteDirectoryRestore: %v", err)
	}

	// Schema group first, then data group in order
	wantOrder := []string{
		"schema/db1.sql",
		"data/db1.tbl1.0001.sql",
		"data/db1.tbl1.0002.sql",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("expected %d entries restored, got %d: %v", len(wantOrder), len(order), order)
	}
	for i, want := range wantOrder {
		if order[i] != want {
			t.Fatalf("order[%d]: expected %q, got %q", i, want, order[i])
		}
	}
}

// ---------------------------------------------------------------------------
// ExecuteDirectoryRestore — policy: mount parallel
// ---------------------------------------------------------------------------

func TestExecuteDirectoryRestore_MountParallel_AllGroupsConcurrent(t *testing.T) {
	t.Parallel()

	// Build a plan with 3 independent data groups, one entry each.
	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "tbl1"},
			{Path: "data/tbl2.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl2"},
			{Path: "data/tbl3.sql", Class: StreamEntryClassData, OrderHint: 3, GroupHint: "tbl3"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	numGroups := len(plan.Groups) // 3
	started := make(chan string, numGroups)
	gate := make(chan struct{})

	fn := func(ctx context.Context, entry StreamEntryIndex) error {
		started <- entry.GroupHint
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- ExecuteDirectoryRestore(context.Background(), plan, RestorePolicyMountParallel, fn)
	}()

	// Collect all started signals with a timeout — if sequential, this will deadlock
	seen := make(map[string]bool)
	timeout := time.After(5 * time.Second)
	for len(seen) < numGroups {
		select {
		case hint := <-started:
			seen[hint] = true
		case <-timeout:
			close(gate) // unblock to avoid goroutine leak
			t.Fatalf("timeout: only %d/%d groups started — suggests sequential execution", len(seen), numGroups)
		}
	}
	// All groups are running concurrently — release the gate
	close(gate)

	if err := <-done; err != nil {
		t.Fatalf("ExecuteDirectoryRestore: %v", err)
	}
}

func TestExecuteDirectoryRestore_MountParallel_WithinGroupOrderPreserved(t *testing.T) {
	t.Parallel()

	// One group with 3 shards — must be restored in OrderHint order
	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.0003.sql", Class: StreamEntryClassData, OrderHint: 3, GroupHint: "tbl1"},
			{Path: "data/tbl1.0001.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "tbl1"},
			{Path: "data/tbl1.0002.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	var mu sync.Mutex
	var order []uint32

	fn := func(_ context.Context, entry StreamEntryIndex) error {
		mu.Lock()
		order = append(order, entry.OrderHint)
		mu.Unlock()
		return nil
	}

	if err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicyMountParallel, fn); err != nil {
		t.Fatalf("ExecuteDirectoryRestore: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(order))
	}
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatalf("within-group order violated: order[%d]=%d < order[%d]=%d", i, order[i], i-1, order[i-1])
		}
	}
}

func TestExecuteDirectoryRestore_MountParallel_ErrorCancelsOthers(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "tbl1"},
			{Path: "data/tbl2.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl2"},
			{Path: "data/tbl3.sql", Class: StreamEntryClassData, OrderHint: 3, GroupHint: "tbl3"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	var callCount int64
	sentinel := errors.New("restore failed")

	fn := func(ctx context.Context, entry StreamEntryIndex) error {
		atomic.AddInt64(&callCount, 1)
		if entry.GroupHint == "tbl1" {
			return sentinel
		}
		// Other groups block until ctx is cancelled
		<-ctx.Done()
		return ctx.Err()
	}

	err = ExecuteDirectoryRestore(context.Background(), plan, RestorePolicyMountParallel, fn)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, sentinel) && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ExecuteDirectoryRestore — policy: dump best-effort
// ---------------------------------------------------------------------------

func TestExecuteDirectoryRestore_DumpBestEffort_SchemaBeforeData(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl1"},
			{Path: "schema/db1.sql", Class: StreamEntryClassSchema, OrderHint: 1, GroupHint: "db1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	var mu sync.Mutex
	var classOrder []StreamEntryClass

	fn := func(_ context.Context, entry StreamEntryIndex) error {
		mu.Lock()
		classOrder = append(classOrder, entry.Class)
		mu.Unlock()
		return nil
	}

	if err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicyDumpBestEffort, fn); err != nil {
		t.Fatalf("ExecuteDirectoryRestore: %v", err)
	}

	if len(classOrder) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(classOrder))
	}
	// Schema must come before data
	if classOrder[0] != StreamEntryClassSchema {
		t.Fatalf("expected schema first, got class %d", classOrder[0])
	}
	if classOrder[1] != StreamEntryClassData {
		t.Fatalf("expected data second, got class %d", classOrder[1])
	}
}

func TestExecuteDirectoryRestore_DumpBestEffort_DataGroupsConcurrentWithinPhase(t *testing.T) {
	t.Parallel()

	// Two data groups — within the data phase they should run concurrently
	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "tbl1"},
			{Path: "data/tbl2.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl2"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	numGroups := 2
	started := make(chan string, numGroups)
	gate := make(chan struct{})

	fn := func(ctx context.Context, entry StreamEntryIndex) error {
		started <- entry.GroupHint
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- ExecuteDirectoryRestore(context.Background(), plan, RestorePolicyDumpBestEffort, fn)
	}()

	seen := make(map[string]bool)
	timeout := time.After(5 * time.Second)
	for len(seen) < numGroups {
		select {
		case hint := <-started:
			seen[hint] = true
		case <-timeout:
			close(gate)
			t.Fatalf("timeout: only %d/%d data groups started concurrently", len(seen), numGroups)
		}
	}
	close(gate)

	if err := <-done; err != nil {
		t.Fatalf("ExecuteDirectoryRestore: %v", err)
	}
}

func TestExecuteDirectoryRestore_DumpBestEffort_SequentialFallbackForShards(t *testing.T) {
	t.Parallel()

	// One data group with multiple shards — shards must run sequentially
	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.0001.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "tbl1"},
			{Path: "data/tbl1.0002.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl1"},
			{Path: "data/tbl1.0003.sql", Class: StreamEntryClassData, OrderHint: 3, GroupHint: "tbl1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	var mu sync.Mutex
	var order []uint32

	fn := func(_ context.Context, entry StreamEntryIndex) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, entry.OrderHint)
		return nil
	}

	if err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicyDumpBestEffort, fn); err != nil {
		t.Fatalf("ExecuteDirectoryRestore: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(order))
	}
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatalf("shard order violated: order[%d]=%d before order[%d]=%d", i-1, order[i-1], i, order[i])
		}
	}
}

// ---------------------------------------------------------------------------
// ExecuteDirectoryRestore — error and cancellation handling
// ---------------------------------------------------------------------------

func TestExecuteDirectoryRestore_ContextCancellation(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "data/tbl1.sql", Class: StreamEntryClassData, OrderHint: 1, GroupHint: "tbl1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	fn := func(ctx context.Context, entry StreamEntryIndex) error {
		return fmt.Errorf("should not be called: %w", ctx.Err())
	}

	err = ExecuteDirectoryRestore(ctx, plan, RestorePolicyMountParallel, fn)
	if err == nil {
		t.Fatalf("expected error for cancelled context")
	}
}

func TestExecuteDirectoryRestore_RejectsNilPlan(t *testing.T) {
	t.Parallel()

	err := ExecuteDirectoryRestore(context.Background(), nil, RestorePolicySequential, func(_ context.Context, _ StreamEntryIndex) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected error for nil plan")
	}
}

func TestExecuteDirectoryRestore_RejectsNilFn(t *testing.T) {
	t.Parallel()

	plan := &DirectoryRestorePlan{
		Groups: []DirectoryRestoreGroup{
			{GroupHint: "g", Entries: []StreamEntryIndex{{Path: "a.sql", Class: StreamEntryClassData, OrderHint: 1}}},
		},
	}

	err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicySequential, nil)
	if err == nil {
		t.Fatalf("expected error for nil fn")
	}
}

func TestExecuteDirectoryRestore_EmptyPlanSucceeds(t *testing.T) {
	t.Parallel()

	plan := &DirectoryRestorePlan{}
	err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicySequential, func(_ context.Context, _ StreamEntryIndex) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error for empty plan, got: %v", err)
	}
}

func TestExecuteDirectoryRestore_UnknownPolicyErrors(t *testing.T) {
	t.Parallel()

	plan := &DirectoryRestorePlan{
		Groups: []DirectoryRestoreGroup{
			{GroupHint: "g", Entries: []StreamEntryIndex{{Path: "a.sql", Class: StreamEntryClassData, OrderHint: 1}}},
		},
	}

	err := ExecuteDirectoryRestore(context.Background(), plan, RestorePolicy(99), func(_ context.Context, _ StreamEntryIndex) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected error for unknown policy")
	}
}

func TestExecuteDirectoryRestore_Sequential_ErrorPropagation(t *testing.T) {
	t.Parallel()

	preflight := &StreamPreflight{
		Mode: StreamModeDirectory,
		Entries: []StreamEntryIndex{
			{Path: "schema/db1.sql", Class: StreamEntryClassSchema, OrderHint: 1, GroupHint: "db1"},
			{Path: "data/tbl1.sql", Class: StreamEntryClassData, OrderHint: 2, GroupHint: "tbl1"},
		},
	}

	plan, err := BuildDirectoryRestorePlan(preflight)
	if err != nil {
		t.Fatalf("BuildDirectoryRestorePlan: %v", err)
	}

	sentinel := errors.New("schema restore failed")
	var callCount int64

	fn := func(_ context.Context, entry StreamEntryIndex) error {
		atomic.AddInt64(&callCount, 1)
		if entry.Class == StreamEntryClassSchema {
			return sentinel
		}
		return nil
	}

	err = ExecuteDirectoryRestore(context.Background(), plan, RestorePolicySequential, fn)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got: %v", err)
	}

	// Data entry should never have been called
	if n := atomic.LoadInt64(&callCount); n != 1 {
		t.Fatalf("expected 1 fn call (schema only), got %d", n)
	}
}
