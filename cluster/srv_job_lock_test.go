// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestJobLockBasic tests basic job lock acquire and release
func TestJobLockBasic(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	// First acquisition should succeed
	if !server.TryAcquireJobLock() {
		t.Fatal("First TryAcquireJobLock should succeed")
	}

	// Second acquisition should fail while first is held
	if server.TryAcquireJobLock() {
		t.Fatal("Second TryAcquireJobLock should fail while lock is held")
	}

	// Release the lock
	server.ReleaseJobLock()

	// After release, acquisition should succeed again
	if !server.TryAcquireJobLock() {
		t.Fatal("TryAcquireJobLock should succeed after release")
	}

	server.ReleaseJobLock()
}

// TestJobLockRaceCondition tests for race conditions in job locking
func TestJobLockRaceCondition(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	const numGoroutines = 100
	const iterations = 10

	var successCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if server.TryAcquireJobLock() {
					successCount.Add(1)
					// Simulate some work
					time.Sleep(time.Millisecond)
					server.ReleaseJobLock()
				}
			}
		}()
	}

	wg.Wait()

	// Only one goroutine at a time should acquire the lock
	// so success count should be reasonable but not exceed safe bounds
	count := successCount.Load()
	t.Logf("Successful lock acquisitions: %d out of %d attempts", count, numGoroutines*iterations)

	if count == 0 {
		t.Fatal("No goroutine was able to acquire the lock")
	}

	// Verify lock is released after all goroutines complete
	if server.IsRunningJobs {
		t.Fatal("Lock should be released after all goroutines complete")
	}
}

// TestJobLockConcurrentAttempts tests concurrent lock attempts
func TestJobLockConcurrentAttempts(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	const numGoroutines = 50
	var acquired atomic.Int32
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Wait for all goroutines to be ready
			<-startCh
			if server.TryAcquireJobLock() {
				acquired.Add(1)
				time.Sleep(10 * time.Millisecond)
				server.ReleaseJobLock()
			}
		}()
	}

	// Start all goroutines at once
	close(startCh)
	wg.Wait()

	// Only one should have succeeded initially
	if acquired.Load() < 1 {
		t.Fatal("At least one goroutine should have acquired the lock")
	}

	t.Logf("Goroutines that acquired lock: %d out of %d", acquired.Load(), numGoroutines)
}

// TestJobLockMultipleReleases tests that multiple releases don't cause issues
func TestJobLockMultipleReleases(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	// Acquire lock
	if !server.TryAcquireJobLock() {
		t.Fatal("Should be able to acquire lock")
	}

	// Release once
	server.ReleaseJobLock()

	// Multiple releases should not panic or cause issues
	server.ReleaseJobLock()
	server.ReleaseJobLock()

	// Should still be able to acquire after multiple releases
	if !server.TryAcquireJobLock() {
		t.Fatal("Should be able to acquire lock after multiple releases")
	}

	server.ReleaseJobLock()
}

// TestJobLockStressTest tests the lock under heavy load
func TestJobLockStressTest(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	const numGoroutines = 200
	const duration = 2 * time.Second

	var operations atomic.Int64
	var errors atomic.Int32
	stopCh := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					if server.TryAcquireJobLock() {
						operations.Add(1)
						// Verify we actually have the lock
						if !server.IsRunningJobs {
							errors.Add(1)
						}
						// Minimal work
						time.Sleep(100 * time.Microsecond)
						server.ReleaseJobLock()
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	// Let it run for a while
	time.Sleep(duration)
	close(stopCh)
	wg.Wait()

	t.Logf("Completed %d operations", operations.Load())
	if errors.Load() > 0 {
		t.Fatalf("Detected %d lock consistency errors", errors.Load())
	}

	// Verify final state
	if server.IsRunningJobs {
		t.Fatal("Lock should be released at the end")
	}
}

// TestJobLockAcquireReleaseSequential tests sequential acquire/release patterns
func TestJobLockAcquireReleaseSequential(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	// Test many sequential acquire/release cycles
	for i := 0; i < 1000; i++ {
		if !server.TryAcquireJobLock() {
			t.Fatalf("Failed to acquire lock on iteration %d", i)
		}
		if !server.IsRunningJobs {
			t.Fatalf("IsRunningJobs should be true after acquire on iteration %d", i)
		}
		server.ReleaseJobLock()
		if server.IsRunningJobs {
			t.Fatalf("IsRunningJobs should be false after release on iteration %d", i)
		}
	}
}

// TestJobLockNoDeadlock tests that the lock doesn't cause deadlocks
func TestJobLockNoDeadlock(t *testing.T) {
	server := &ServerMonitor{
		IsRunningJobs: false,
	}

	done := make(chan bool, 1)
	timeout := time.After(5 * time.Second)

	go func() {
		// Acquire and release many times
		for i := 0; i < 100; i++ {
			server.TryAcquireJobLock()
			time.Sleep(time.Millisecond)
			server.ReleaseJobLock()
		}
		done <- true
	}()

	select {
	case <-done:
		t.Log("No deadlock detected")
	case <-timeout:
		t.Fatal("Test timed out - possible deadlock")
	}
}
