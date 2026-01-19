package backupmgr

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/protobuf/runtime/protoimpl"
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

const (
	BackupLineDefault = "default"
	BackupLineAdhoc   = "adhoc"
)

type BackupMetadata struct {
	Id                int64          `json:"id"`
	StartTime         time.Time      `json:"startTime"`
	EndTime           time.Time      `json:"endTime"`
	BackupMethod      BackupMethod   `json:"backupMethod"`
	BackupTool        string         `json:"backupTool"`
	BackupToolVersion string         `json:"backupToolVersion"`
	BackupStrategy    BackupStrategy `json:"backupStrategy"`
	Source            string         `json:"source"`
	Dest              string         `json:"dest"`
	Size              int64          `json:"size"`
	FileCount         int64          `json:"fileCount"`
	Compressed        bool           `json:"compressed"`
	Encrypted         bool           `json:"encrypted"`
	EncryptionAlgo    string         `json:"encryptionAlgo"`
	EncryptionKey     string         `json:"encryptionKey"`
	Checksum          string         `json:"checksum"`
	RetentionDays     int            `json:"retentionDays"`
	BackupLine        string         `json:"backupLine,omitempty"`
	MetaFile          string         `json:"metaFile,omitempty"`
	BinLogFileName    string         `json:"binLogFileName"`
	BinLogFilePos     uint64         `json:"binLogFilePos"`
	BinLogGtid        string         `json:"binLogUuid"`
	Completed         bool           `json:"completed"`
	SplitUser         bool           `json:"splitUser"`
	Previous          int64          `json:"previous"`
	ResticEnabled     bool           `json:"resticEnabled,omitempty"`
	ResticSnapshotID  string         `json:"resticSnapshotID"`
	ResticFilePath    string         `json:"resticFilePath"`
}

type PointInTimeMeta struct {
	IsInPITR    bool
	UseBinlog   bool
	Backup      int64
	RestoreTime int64
}

func (bm *BackupMetadata) GetSizeAndFileCount() error {
	var size int64 = 0
	var fileCount int64 = 0
	err := filepath.Walk(bm.Dest, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
			fileCount++
		}
		return err
	})
	bm.Size = size
	bm.FileCount = fileCount
	return err
}

func (bm *BackupMetadata) IsAdhoc() bool {
	if bm == nil {
		return false
	}
	if bm.BackupLine == BackupLineAdhoc {
		return true
	}
	return bm.RetentionDays > 0
}

type ReadBinaryLogsBoundary struct {
	UseTimestamp bool
	Filename     string
	Position     int64
	Timestamp    time.Time
}

type ReadBinaryLogsRange struct {
	Start ReadBinaryLogsBoundary
	End   ReadBinaryLogsBoundary
}

type BackupStat struct {
	TotalSize      int64 `protobuf:"varint,1,opt,name=total_size,proto3" json:"total_size"`
	TotalFileCount int64 `protobuf:"varint,2,opt,name=total_file_count,proto3" json:"total_file_count"`
	TotalBlobCount int64 `protobuf:"varint,3,opt,name=total_blob_count,proto3" json:"total_blob_count"`
}

type BackupSnapshot struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id       string   `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	ShortId  string   `protobuf:"bytes,2,opt,name=short_id,proto3" json:"short_id,omitempty"`
	Time     string   `protobuf:"bytes,3,opt,name=time,proto3" json:"time,omitempty"`
	Tree     string   `protobuf:"bytes,4,opt,name=tree,proto3" json:"tree,omitempty"`
	Paths    []string `protobuf:"bytes,5,rep,name=paths,proto3" json:"paths,omitempty"`
	Hostname string   `protobuf:"bytes,6,opt,name=hostname,proto3" json:"hostname,omitempty"`
	Username string   `protobuf:"bytes,7,opt,name=username,proto3" json:"username,omitempty"`
	Uid      int64    `protobuf:"varint,8,opt,name=uid,proto3" json:"uid,omitempty"`
	Gid      int64    `protobuf:"varint,9,opt,name=gid,proto3" json:"gid,omitempty"`
	Tags     []string `protobuf:"bytes,10,rep,name=tags,proto3" json:"tags,omitempty"`
}

type BackupMetaMap struct {
	*sync.Map
}

func NewBackupMetaMap() *BackupMetaMap {
	s := new(sync.Map)
	m := &BackupMetaMap{Map: s}
	return m
}

func (m *BackupMetaMap) Get(key int64) *BackupMetadata {
	if v, ok := m.Load(key); ok {
		return v.(*BackupMetadata)
	}
	return nil
}

func (m *BackupMetaMap) CheckAndGet(key int64) (*BackupMetadata, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*BackupMetadata), true
	}
	return nil, false
}

func (m *BackupMetaMap) Set(key int64, value *BackupMetadata) {
	m.Store(key, value)
}

func (m *BackupMetaMap) ToNormalMap(c map[int64]*BackupMetadata) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the BackupMetaMap to the output map
	m.Callback(func(key int64, value *BackupMetadata) bool {
		c[key] = value
		return true
	})
}

func (m *BackupMetaMap) ToNewMap() map[int64]*BackupMetadata {
	result := make(map[int64]*BackupMetadata)
	m.Range(func(k, v any) bool {
		result[k.(int64)] = v.(*BackupMetadata)
		return true
	})
	return result
}

func (m *BackupMetaMap) Callback(f func(key int64, value *BackupMetadata) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(int64), v.(*BackupMetadata))
	})
}

func (m *BackupMetaMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(int64))
		return true
	})
}

func FromNormalBackupMetaMap(m *BackupMetaMap, c map[int64]*BackupMetadata) *BackupMetaMap {
	if m == nil {
		m = NewBackupMetaMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromBackupMetaMap(m *BackupMetaMap, c *BackupMetaMap) *BackupMetaMap {
	if m == nil {
		m = NewBackupMetaMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key int64, value *BackupMetadata) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

// GetBackupsByToolAndSource retrieves backups with the same backupTool and source.
func (b *BackupMetaMap) GetPreviousBackup(backupTool string, source string) *BackupMetadata {
	var result *BackupMetadata
	b.Map.Range(func(key, value interface{}) bool {
		if backup, ok := value.(*BackupMetadata); ok {
			if backup.IsAdhoc() {
				return true
			}
			if backup.BackupTool == backupTool && backup.Source == source {
				result = backup
				return false
			}
		}
		return true
	})
	return result
}
