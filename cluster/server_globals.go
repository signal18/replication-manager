package cluster

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// ServerGlobals holds instance-wide shared resources passed from the server
// to all clusters at init time. This avoids import cycles between server/
// and cluster/ while giving clusters access to global coordination primitives.
type ServerGlobals struct {
	// BackupSemaphore limits the number of concurrent backups (logical or
	// physical) across all clusters. Sized by backup-concurrent-slots.
	// A cluster acquires a slot before starting a backup and releases it on
	// completion. Nil when no limit is configured (0 = unlimited).
	BackupSemaphore chan struct{}
}

// AcquireBackupSlot blocks until a backup slot is available.
// Returns immediately if no semaphore is configured.
func (sg *ServerGlobals) AcquireBackupSlot() {
	if sg == nil || sg.BackupSemaphore == nil {
		return
	}
	sg.BackupSemaphore <- struct{}{}
}

// ReleaseBackupSlot frees a backup slot.
// Safe to call even if no semaphore is configured.
func (sg *ServerGlobals) ReleaseBackupSlot() {
	if sg == nil || sg.BackupSemaphore == nil {
		return
	}
	<-sg.BackupSemaphore
}

// waitForBackupSlot opens a WARN0174 state while waiting for a global backup
// slot, then clears it once the slot is acquired. Returns false if the
// cluster is shutting down before a slot could be acquired.
func (cluster *Cluster) waitForBackupSlot() bool {
	if cluster.ServerGlobals == nil || cluster.ServerGlobals.BackupSemaphore == nil {
		return true
	}
	inUse := len(cluster.ServerGlobals.BackupSemaphore)
	total := cap(cluster.ServerGlobals.BackupSemaphore)
	if inUse >= total {
		cluster.SetState("WARN0174", state.State{
			ErrType: "WARNING",
			ErrDesc: fmt.Sprintf(config.ClusterError["WARN0174"], inUse, total),
			ErrFrom: "BACKUP",
		})
	}
	for {
		select {
		case cluster.ServerGlobals.BackupSemaphore <- struct{}{}:
			return true
		case <-time.After(2 * time.Second):
			if cluster.exit.Load() {
				return false
			}
			inUse = len(cluster.ServerGlobals.BackupSemaphore)
			total = cap(cluster.ServerGlobals.BackupSemaphore)
			cluster.SetState("WARN0174", state.State{
				ErrType: "WARNING",
				ErrDesc: fmt.Sprintf(config.ClusterError["WARN0174"], inUse, total),
				ErrFrom: "BACKUP",
			})
		}
	}
}
