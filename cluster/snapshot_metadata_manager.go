package cluster

import "sync"

type snapshotMetadataManager struct {
	cluster *Cluster

	cache                   *snapshotMetadataCache
	extractorSem            chan struct{}
	extractorSemMu          sync.Mutex
	extractorSemCap         int
	extractorSemPending     int
	resticMetadataDir       string
	resticSnapshotLsCache   map[string]map[string]bool
	resticSnapshotLsCacheMu sync.Mutex
}

func newSnapshotMetadataManager(cluster *Cluster) *snapshotMetadataManager {
	manager := &snapshotMetadataManager{cluster: cluster}
	manager.cache = newSnapshotMetadataCache()
	return manager
}
