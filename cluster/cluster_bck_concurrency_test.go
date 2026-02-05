package cluster

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Run with: go test -race ./cluster/...
func TestSnapshotMetadataCacheConcurrentAccess(t *testing.T) {
	cache := newSnapshotMetadataCache()
	if cache == nil {
		t.Fatalf("expected snapshot metadata cache to be initialized")
	}

	snapshotIDs := []string{"snap-a", "snap-b", "snap-c"}
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
	}

	for i := 0; i < 64; i++ {
		idx := i
		snapshotID := snapshotIDs[i%len(snapshotIDs)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
				entry.Status = snapshotMetadataStatusReady
				entry.LastAttempt = time.Now()
				key := fmt.Sprintf("key-%d", idx)
				entry.Summaries[key] = &SnapshotMetadataSummary{
					ResticSnapshotID: snapshotID,
					BackupSessionID:  fmt.Sprintf("session-%d", idx),
					StartTime:        time.Now(),
				}
			})
		}()
	}

	for i := 0; i < 64; i++ {
		snapshotID := snapshotIDs[i%len(snapshotIDs)]
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				entry, _ := cache.Get(id)
				if entry != nil && entry.Summaries == nil {
					recordErr(fmt.Errorf("expected summaries map for %s", id))
					return
				}
				_ = cache.GetSummaries(id)
				_ = cache.SnapshotIDs()
			}
		}(snapshotID)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent access error: %v", err)
		}
	}

	entry, ok := cache.Get(snapshotIDs[0])
	if !ok || entry == nil || len(entry.Summaries) == 0 {
		t.Fatalf("expected cache entry for %s", snapshotIDs[0])
	}
	for _, summary := range entry.Summaries {
		if summary != nil {
			summary.BackupSessionID = "mutated"
		}
	}
	entry2, ok := cache.Get(snapshotIDs[0])
	if !ok || entry2 == nil {
		t.Fatalf("expected cache entry to remain for %s", snapshotIDs[0])
	}
	for _, summary := range entry2.Summaries {
		if summary != nil && summary.BackupSessionID == "mutated" {
			t.Fatalf("expected cache entry to return a clone")
		}
	}
}
