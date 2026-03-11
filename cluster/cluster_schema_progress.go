package cluster

import "time"

func (cluster *Cluster) schemaMonitorReset() {
	cluster.Lock()
	cluster.SchemaMonitorProgress = SchemaMonitorProgress{
		Status:    "running",
		Phase:     "list",
		StartTime: time.Now().Unix(),
	}
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorSetPhase(phase string) {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Phase = phase
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorSetMasterTotals(totalTables int, totalBytes int64) {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Master.TotalTables = totalTables
	cluster.SchemaMonitorProgress.Master.TotalBytes = totalBytes
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorSetSlaveTotals(totalTables int, totalBytes int64) {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Slaves.TotalTables = totalTables
	cluster.SchemaMonitorProgress.Slaves.TotalBytes = totalBytes
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorResetMasterProgress() {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Master.ProcessedTables = 0
	cluster.SchemaMonitorProgress.Master.ProcessedBytes = 0
	cluster.SchemaMonitorProgress.Master.Percent = 0
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorResetSlaveProgress() {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Slaves.ProcessedTables = 0
	cluster.SchemaMonitorProgress.Slaves.ProcessedBytes = 0
	cluster.SchemaMonitorProgress.Slaves.Percent = 0
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorUpdateMasterProgress(processedTables int, processedBytes int64) {
	cluster.Lock()
	progress := &cluster.SchemaMonitorProgress.Master
	progress.ProcessedTables = processedTables
	progress.ProcessedBytes = processedBytes
	progress.Percent = schemaMonitorPercent(progress.ProcessedTables, progress.TotalTables, progress.ProcessedBytes, progress.TotalBytes)
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorUpdateSlaveProgress(processedTables int, processedBytes int64, currentSlave string) {
	cluster.Lock()
	progress := &cluster.SchemaMonitorProgress.Slaves
	progress.ProcessedTables = processedTables
	progress.ProcessedBytes = processedBytes
	progress.Percent = schemaMonitorPercent(progress.ProcessedTables, progress.TotalTables, progress.ProcessedBytes, progress.TotalBytes)
	cluster.SchemaMonitorProgress.CurrentSlave = currentSlave
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorError(err error) {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Status = "error"
	cluster.SchemaMonitorProgress.EndTime = time.Now().Unix()
	if err != nil {
		cluster.SchemaMonitorProgress.LastError = err.Error()
	}
	cluster.Unlock()
}

func (cluster *Cluster) schemaMonitorDone() {
	cluster.Lock()
	cluster.SchemaMonitorProgress.Status = "done"
	cluster.SchemaMonitorProgress.Phase = "idle"
	cluster.SchemaMonitorProgress.EndTime = time.Now().Unix()
	cluster.Unlock()
}

func schemaMonitorPercent(processedTables, totalTables int, processedBytes, totalBytes int64) float64 {
	if totalBytes > 0 {
		return float64(processedBytes) * 100 / float64(totalBytes)
	}
	if totalTables > 0 {
		return float64(processedTables) * 100 / float64(totalTables)
	}
	return 0
}
