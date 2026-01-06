// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// TestMySQLDefaultsNoDeadlock tests for potential deadlocks in concurrent operations
func TestMySQLDefaultsNoDeadlock(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mysql-defaults-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a mock cluster
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		mysqlDefaultValues:       make(map[string]string),
		mysqlDefaultValuesLoaded: false,
	}

	// Test 1: Concurrent reads should not deadlock
	t.Run("ConcurrentReads", func(t *testing.T) {
		done := make(chan bool, 10)
		timeout := time.After(5 * time.Second)

		for i := 0; i < 10; i++ {
			go func() {
				_, _ = cluster.GetMySQLDefaultsCnfContent()
				done <- true
			}()
		}

		for i := 0; i < 10; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in concurrent reads")
			}
		}
	})

	// Test 2: Concurrent writes should not deadlock
	t.Run("ConcurrentWrites", func(t *testing.T) {
		done := make(chan bool, 5)
		timeout := time.After(5 * time.Second)

		for i := 0; i < 5; i++ {
			go func(idx int) {
				content := "# Test content\n[mysqld]\nmax_connections = 100\n"
				_ = cluster.WriteMySQLDefaultsCnfContent(content)
				done <- true
			}(i)
		}

		for i := 0; i < 5; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in concurrent writes")
			}
		}
	})

	// Test 3: Mixed reads and writes should not deadlock
	t.Run("MixedReadWrite", func(t *testing.T) {
		done := make(chan bool, 20)
		timeout := time.After(5 * time.Second)

		// Start 10 readers
		for i := 0; i < 10; i++ {
			go func() {
				_, _ = cluster.GetMySQLDefaultsCnfContent()
				done <- true
			}()
		}

		// Start 10 writers
		for i := 0; i < 10; i++ {
			go func() {
				content := "# Test content\n[mysqld]\nmax_connections = 100\n"
				_ = cluster.WriteMySQLDefaultsCnfContent(content)
				done <- true
			}()
		}

		for i := 0; i < 20; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in mixed read/write operations")
			}
		}
	})

	// Test 4: GetInfo and Reload operations should not deadlock
	t.Run("InfoAndReload", func(t *testing.T) {
		done := make(chan bool, 20)
		timeout := time.After(5 * time.Second)

		for i := 0; i < 10; i++ {
			go func() {
				_ = cluster.GetMySQLDefaultsInfo()
				done <- true
			}()
		}

		for i := 0; i < 10; i++ {
			go func() {
				_ = cluster.ReloadMySQLDefaults()
				done <- true
			}()
		}

		for i := 0; i < 20; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in GetInfo/Reload operations")
			}
		}
	})

	// Test 5: SaveMySQLDefaults should not deadlock
	t.Run("SaveDefaults", func(t *testing.T) {
		// Initialize defaults first
		_ = cluster.initMySQLDefaults()

		done := make(chan bool, 5)
		timeout := time.After(5 * time.Second)

		for i := 0; i < 5; i++ {
			go func() {
				_ = cluster.SaveMySQLDefaults()
				done <- true
			}()
		}

		for i := 0; i < 5; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in SaveMySQLDefaults")
			}
		}
	})

	// Test 6: All operations together (stress test)
	t.Run("StressTest", func(t *testing.T) {
		var wg sync.WaitGroup
		timeout := time.After(10 * time.Second)
		done := make(chan bool)

		operations := []func(){
			func() { _, _ = cluster.GetMySQLDefaultsCnfContent() },
			func() { _ = cluster.WriteMySQLDefaultsCnfContent("# Test\n[mysqld]\nmax_connections = 100\n") },
			func() { _ = cluster.GetMySQLDefaultsInfo() },
			func() { _ = cluster.ReloadMySQLDefaults() },
			func() { _ = cluster.SaveMySQLDefaults() },
			func() { _ = cluster.getMySQLDefaultForVar("max_connections") },
		}

		// Run 30 random operations concurrently
		wg.Add(30)
		for i := 0; i < 30; i++ {
			go func(idx int) {
				defer wg.Done()
				op := operations[idx%len(operations)]
				op()
			}(i)
		}

		go func() {
			wg.Wait()
			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-timeout:
			t.Fatal("Deadlock detected in stress test")
		}
	})
}

// TestReloadMySQLDefaultsUnsafe tests the specific deadlock scenario in reloadMySQLDefaultsUnsafe
func TestReloadMySQLDefaultsUnsafe(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mysql-defaults-unsafe-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		mysqlDefaultValues:       make(map[string]string),
		mysqlDefaultValuesLoaded: false,
	}

	// Test the specific scenario where reloadMySQLDefaultsUnsafe
	// temporarily releases RLock to call SaveMySQLDefaults (which takes RLock)
	t.Run("UnlockRelockScenario", func(t *testing.T) {
		timeout := time.After(5 * time.Second)
		done := make(chan bool)

		go func() {
			// This should trigger the unlock/relock path in reloadMySQLDefaultsUnsafe
			// when the file doesn't exist initially
			_ = cluster.ReloadMySQLDefaults()
			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-timeout:
			t.Fatal("Deadlock detected in unlock/relock scenario")
		}
	})
}

// TestGetMySQLDefaultsCnfContentAutoCreate tests auto-creation deadlock scenario
func TestGetMySQLDefaultsCnfContentAutoCreate(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mysql-defaults-autocreate-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		mysqlDefaultValues:       make(map[string]string),
		mysqlDefaultValuesLoaded: false,
	}

	// Test concurrent auto-creation from embedded defaults
	t.Run("ConcurrentAutoCreate", func(t *testing.T) {
		done := make(chan bool, 5)
		timeout := time.After(5 * time.Second)

		// Multiple goroutines try to read non-existent file at the same time
		// This triggers auto-creation from embedded defaults
		for i := 0; i < 5; i++ {
			go func() {
				_, _ = cluster.GetMySQLDefaultsCnfContent()
				done <- true
			}()
		}

		for i := 0; i < 5; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in concurrent auto-create")
			}
		}
	})
}

// TestSaveMySQLDefaultsWithRLock tests SaveMySQLDefaults which uses RLock
func TestSaveMySQLDefaultsWithRLock(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mysql-defaults-rlock-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		mysqlDefaultValues: map[string]string{
			"MAX_CONNECTIONS": "100",
		},
		mysqlDefaultValuesLoaded: true,
	}

	// Test that SaveMySQLDefaults (RLock) doesn't deadlock with other operations
	t.Run("SaveWithConcurrentReads", func(t *testing.T) {
		done := make(chan bool, 10)
		timeout := time.After(5 * time.Second)

		// 5 Save operations (RLock)
		for i := 0; i < 5; i++ {
			go func() {
				_ = cluster.SaveMySQLDefaults()
				done <- true
			}()
		}

		// 5 GetInfo operations (RLock)
		for i := 0; i < 5; i++ {
			go func() {
				_ = cluster.GetMySQLDefaultsInfo()
				done <- true
			}()
		}

		for i := 0; i < 10; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected with SaveMySQLDefaults and concurrent reads")
			}
		}
	})
}

// TestMutexOperationOrder tests the order of mutex operations
func TestMutexOperationOrder(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mysql-defaults-order-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		mysqlDefaultValues:       make(map[string]string),
		mysqlDefaultValuesLoaded: false,
	}

	// Create the file first to avoid auto-creation path
	defaultsPath := cluster.GetMySQLDefaultsPath()
	_ = os.MkdirAll(filepath.Dir(defaultsPath), 0755)
	_ = os.WriteFile(defaultsPath, []byte("# Test\n[mysqld]\nmax_connections = 100\n"), 0644)

	t.Run("OperationSequence", func(t *testing.T) {
		timeout := time.After(5 * time.Second)
		done := make(chan bool)

		go func() {
			// Sequence that might cause deadlock:
			// 1. GetMySQLDefaultsCnfContent (no lock, but reads file)
			// 2. WriteMySQLDefaultsCnfContent (writes file, calls ReloadMySQLDefaults with Lock)
			// 3. GetMySQLDefaultsInfo (RLock)
			// 4. SaveMySQLDefaults (RLock)
			// 5. ReloadMySQLDefaults (Lock)

			_, _ = cluster.GetMySQLDefaultsCnfContent()
			_ = cluster.WriteMySQLDefaultsCnfContent("# Test\n[mysqld]\nmax_connections = 200\n")
			_ = cluster.GetMySQLDefaultsInfo()
			_ = cluster.SaveMySQLDefaults()
			_ = cluster.ReloadMySQLDefaults()

			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-timeout:
			t.Fatal("Deadlock detected in operation sequence")
		}
	})
}

// TestWriteThenReadRace tests the race between write and subsequent read
func TestWriteThenReadRace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mysql-defaults-race-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		mysqlDefaultValues:       make(map[string]string),
		mysqlDefaultValuesLoaded: false,
	}

	t.Run("WriteReadRace", func(t *testing.T) {
		timeout := time.After(5 * time.Second)
		done := make(chan bool, 10)

		for i := 0; i < 10; i++ {
			go func(idx int) {
				// Write then immediately read
				_ = cluster.WriteMySQLDefaultsCnfContent("# Test\n[mysqld]\nmax_connections = 100\n")
				_, _ = cluster.GetMySQLDefaultsCnfContent()
				done <- true
			}(i)
		}

		for i := 0; i < 10; i++ {
			select {
			case <-done:
				// Success
			case <-timeout:
				t.Fatal("Deadlock detected in write-then-read race")
			}
		}
	})
}
