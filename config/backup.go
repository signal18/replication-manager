package config

import (
	"os"
	"path/filepath"
	"time"
)

type BackupMethod int

const (
	BackupMethodLogical  = 1
	BackupMethodPhysical = 2
)

type BackupStrategy int

const (
	BackupStrategyFull         = 1
	BackupStrategyIncremental  = 2
	BackupStrategyDifferential = 3
)

type BackupMetadata struct {
	Id             int64          `json:"id"`
	StartTime      time.Time      `json:"startTime"`
	EndTime        time.Time      `json:"endTime"`
	BackupMethod   BackupMethod   `json:"backupMethod"`
	BackupTool     string         `json:"backupTool"`
	BackupStrategy BackupStrategy `json:"backupStrategy"`
	Source         string         `json:"source"`
	Dest           string         `json:"dest"`
	Size           int64          `json:"size"`
	Compressed     bool           `json:"compressed"`
	Encrypted      bool           `json:"encrypted"`
	EncryptionAlgo string         `json:"encryptionAlgo"`
	EncryptionKey  string         `json:"encryptionKey"`
	Checksum       string         `json:"checksum"`
	RetentionDays  int            `json:"retentionDays"`
	BinLogFileName string         `json:"binLogFileName"`
	BinLogFilePos  uint64         `json:"binLogFilePos"`
	BinLogGtid     string         `json:"binLogUuid"`
	Completed      bool           `json:"completed"`
	Previous       int64          `json:"previous"`
}

func (bm *BackupMetadata) GetSize() error {
	var size int64 = 0
	err := filepath.Walk(bm.Dest, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	bm.Size = size
	return err
}
