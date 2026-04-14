package backupmgr

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// DirectoryRestoreGroup holds all entries that share the same GroupHint,
// ordered ascending by OrderHint for sequential restore within the group.
type DirectoryRestoreGroup struct {
	GroupHint string
	Entries   []StreamEntryIndex // sorted ascending by OrderHint
}

// DirectoryRestorePlan organizes stream container entries into restore groups.
// Groups are ordered so that schema-class entries appear before data-class entries,
// which appear before meta-class entries, preserving restore dependency ordering.
type DirectoryRestorePlan struct {
	Groups []DirectoryRestoreGroup
}

// RestorePolicy controls how groups are dispatched during directory restore.
type RestorePolicy uint8

const (
	// RestorePolicyMountParallel dispatches all groups concurrently. Within each
	// group, entries are restored in OrderHint order. Suitable for restic mount
	// which provides random access to any entry.
	RestorePolicyMountParallel RestorePolicy = iota + 1

	// RestorePolicyDumpBestEffort executes groups in class-ordered phases
	// (schema → data → meta → system). Within each phase, groups run concurrently.
	// Within each group, entries run sequentially. Suitable for restic dump where
	// schema must precede data for correctness.
	RestorePolicyDumpBestEffort

	// RestorePolicySequential restores all groups and entries one at a time.
	// Use when parallel access is not available or not correct.
	RestorePolicySequential
)

// EntryRestoreFunc is the callback invoked per entry to perform the restore.
// It must respect context cancellation.
type EntryRestoreFunc func(ctx context.Context, entry StreamEntryIndex) error

// BuildDirectoryRestorePlan groups and sorts the entries from a directory-mode
// stream container preflight. Schema entries are placed before data entries,
// which are placed before meta entries (entryClassTier ordering). Within each
// group, entries are sorted ascending by OrderHint.
//
// Returns an error if preflight is nil, not in directory mode, or has no entries.
func BuildDirectoryRestorePlan(preflight *StreamPreflight) (*DirectoryRestorePlan, error) {
	if preflight == nil {
		return nil, fmt.Errorf("preflight is required")
	}
	if preflight.Mode != StreamModeDirectory {
		return nil, fmt.Errorf("directory restore plan requires directory mode, got mode %d", preflight.Mode)
	}
	if len(preflight.Entries) == 0 {
		return nil, fmt.Errorf("directory restore plan: no entries in container")
	}

	// Group entries by GroupHint, preserving first-seen group order.
	type slot struct {
		order int
		group DirectoryRestoreGroup
	}
	groupIndex := make(map[string]int) // GroupHint → index in slots
	var slots []slot

	for _, entry := range preflight.Entries {
		idx, ok := groupIndex[entry.GroupHint]
		if !ok {
			idx = len(slots)
			groupIndex[entry.GroupHint] = idx
			slots = append(slots, slot{order: idx, group: DirectoryRestoreGroup{GroupHint: entry.GroupHint}})
		}
		slots[idx].group.Entries = append(slots[idx].group.Entries, entry)
	}

	// Sort entries within each group by OrderHint.
	for i := range slots {
		sort.Slice(slots[i].group.Entries, func(a, b int) bool {
			return slots[i].group.Entries[a].OrderHint < slots[i].group.Entries[b].OrderHint
		})
	}

	// Build sorted groups slice.
	groups := make([]DirectoryRestoreGroup, len(slots))
	for i, s := range slots {
		groups[i] = s.group
	}

	// Sort groups by class tier (schema < data < meta < system), then by the
	// minimum OrderHint of the first entry within each group.
	sort.SliceStable(groups, func(i, j int) bool {
		ti := entryClassTier(groups[i].Entries[0].Class)
		tj := entryClassTier(groups[j].Entries[0].Class)
		if ti != tj {
			return ti < tj
		}
		return groups[i].Entries[0].OrderHint < groups[j].Entries[0].OrderHint
	})

	return &DirectoryRestorePlan{Groups: groups}, nil
}

// ExecuteDirectoryRestore runs the restore plan using the given policy and
// restore function. All errors cause immediate cancellation of in-progress work.
// Returns nil when all entries have been successfully restored.
func ExecuteDirectoryRestore(ctx context.Context, plan *DirectoryRestorePlan, policy RestorePolicy, fn EntryRestoreFunc) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if plan == nil {
		return fmt.Errorf("restore plan is required")
	}
	if fn == nil {
		return fmt.Errorf("restore function is required")
	}
	if len(plan.Groups) == 0 {
		return nil
	}

	switch policy {
	case RestorePolicyMountParallel:
		return executeMountParallel(ctx, plan.Groups, fn)
	case RestorePolicyDumpBestEffort:
		return executeDumpBestEffort(ctx, plan.Groups, fn)
	case RestorePolicySequential:
		return executeSequential(ctx, plan.Groups, fn)
	default:
		return fmt.Errorf("unknown restore policy %d", policy)
	}
}

// executeMountParallel dispatches all groups concurrently. Within each group,
// entries are restored sequentially by OrderHint.
func executeMountParallel(ctx context.Context, groups []DirectoryRestoreGroup, fn EntryRestoreFunc) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(groups))
	var wg sync.WaitGroup

	for _, g := range groups {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := restoreGroupSequential(ctx, g, fn); err != nil {
				errCh <- err
				cancel()
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// executeDumpBestEffort runs groups in class-ordered phases: schema, data, meta,
// system. Within each phase, groups are dispatched concurrently (best-effort
// parallelism). Within each group, entries run sequentially to preserve shard
// ordering. Schema phases must complete before data phases begin, satisfying
// the sequential correctness requirement for dump-mode restores.
func executeDumpBestEffort(ctx context.Context, groups []DirectoryRestoreGroup, fn EntryRestoreFunc) error {
	// Partition groups into class tiers.
	tiers := make(map[int][]DirectoryRestoreGroup)
	for _, g := range groups {
		tier := entryClassTier(g.Entries[0].Class)
		tiers[tier] = append(tiers[tier], g)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Execute tiers in class order: schema(0) → data(1) → meta(2) → system(3).
	for _, tier := range []int{0, 1, 2, 3} {
		tierGroups, ok := tiers[tier]
		if !ok {
			continue
		}
		if err := executeGroupsParallel(ctx, cancel, tierGroups, fn); err != nil {
			return err
		}
	}
	return nil
}

// executeSequential restores all groups and entries one at a time.
func executeSequential(ctx context.Context, groups []DirectoryRestoreGroup, fn EntryRestoreFunc) error {
	for _, g := range groups {
		if err := restoreGroupSequential(ctx, g, fn); err != nil {
			return err
		}
	}
	return nil
}

// executeGroupsParallel dispatches all groups in the slice concurrently, waits
// for all to complete, then returns the first error (if any). On any error it
// invokes cancel to stop remaining work.
func executeGroupsParallel(ctx context.Context, cancel context.CancelFunc, groups []DirectoryRestoreGroup, fn EntryRestoreFunc) error {
	if len(groups) == 1 {
		// Avoid goroutine overhead for a single group.
		return restoreGroupSequential(ctx, groups[0], fn)
	}

	errCh := make(chan error, len(groups))
	var wg sync.WaitGroup

	for _, g := range groups {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := restoreGroupSequential(ctx, g, fn); err != nil {
				errCh <- err
				cancel()
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// restoreGroupSequential restores all entries in a group sequentially by OrderHint.
func restoreGroupSequential(ctx context.Context, group DirectoryRestoreGroup, fn EntryRestoreFunc) error {
	for _, entry := range group.Entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := fn(ctx, entry); err != nil {
			return fmt.Errorf("group %q entry %q: %w", group.GroupHint, entry.Path, err)
		}
	}
	return nil
}

// entryClassTier maps StreamEntryClass to a numeric tier for restore ordering.
// Schema(0) < Data(1) < Meta(2) < System(3).
func entryClassTier(class StreamEntryClass) int {
	switch class {
	case StreamEntryClassSchema:
		return 0
	case StreamEntryClassData:
		return 1
	case StreamEntryClassMeta:
		return 2
	case StreamEntryClassSystem:
		return 3
	default:
		return 4
	}
}
