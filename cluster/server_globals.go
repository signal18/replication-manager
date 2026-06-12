package cluster

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
