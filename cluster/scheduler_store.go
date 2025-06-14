package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const schedulerStorageFile = "scheduled_jobs.json"

type FileJobStore struct {
	storagePath string
}

func NewFileJobStore(dataDir string) *FileJobStore {
	return &FileJobStore{
		storagePath: filepath.Join(dataDir, schedulerStorageFile),
	}
}

func (fs *FileJobStore) SaveJobs(jobs []ScheduledJob) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fs.storagePath, data, 0644)
}

func (fs *FileJobStore) LoadJobs() ([]ScheduledJob, error) {
	data, err := os.ReadFile(fs.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ScheduledJob{}, nil
		}
		return nil, err
	}

	var jobs []ScheduledJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}
