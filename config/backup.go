package config

import "time"

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
}
