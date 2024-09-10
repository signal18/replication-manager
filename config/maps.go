package config

import (
	"sync"

	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
)

type StringsMap struct {
	*sync.Map
}

func (m *StringsMap) Get(key string) string {
	if v, ok := m.Load(key); ok {
		return v.(string)
	}
	return ""
}

func (m *StringsMap) CheckAndGet(key string) (string, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(string), true
	}
	return "", false
}

func (m *StringsMap) ToNormalMap(c map[string]string) {
	// clear old value
	c = make(map[string]string)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(string)
		return true
	})
}

func (m *StringsMap) ToNewMap() map[string]string {
	// clear old value
	c := make(map[string]string)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(string)
		return true
	})

	return c
}

func (m *StringsMap) Set(k string, v string) {
	m.Store(k, v)
}

func FromNormalStringMap(m *StringsMap, c map[string]string) *StringsMap {
	if m == nil {
		m = NewStringsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Store(k, v)
	}

	return m
}

func FromStringSyncMap(m *StringsMap, c *StringsMap) *StringsMap {
	if m == nil {
		m = NewStringsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Range(func(k any, v any) bool {
			m.Store(k.(string), v.(string))
			return true
		})
	}

	return m
}

func (m *StringsMap) Callback(f func(key, value any) bool) {
	//Insert all values to new map
	m.Range(f)
}

func (m *StringsMap) Clear() {
	//Insert all values to new map
	m.Range(func(key any, value any) bool {
		k := key.(string)
		m.Delete(k)
		return true
	})
}

func NewStringsMap() *StringsMap {
	s := new(sync.Map)
	m := &StringsMap{Map: s}
	return m
}

type UIntsMap struct {
	*sync.Map
}

func (m *UIntsMap) Get(key string) uint {
	if v, ok := m.Load(key); ok {
		return v.(uint)
	}
	return 0
}

func (m *UIntsMap) CheckAndGet(key string) (uint, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(uint), true
	}
	return 0, false
}

func (m *UIntsMap) ToNormalMap(c map[string]uint) {
	// clear old value
	c = make(map[string]uint)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(uint)
		return true
	})
}

func (m *UIntsMap) ToNewMap() map[string]uint {
	// clear old value
	c := make(map[string]uint)

	//Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(uint)
		return true
	})

	return c
}

func (m *UIntsMap) Set(k string, v uint) {
	m.Store(k, v)
}

func FromNormalUIntsMap(m *UIntsMap, c map[string]uint) *UIntsMap {
	if m == nil {
		m = NewUIntsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Store(k, v)
	}

	return m
}

func FromUIntSyncMap(m *UIntsMap, c *UIntsMap) *UIntsMap {
	if m == nil {
		m = NewUIntsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Range(func(k any, v any) bool {
			m.Store(k.(string), v.(uint))
			return true
		})
	}

	return m
}

func (m *UIntsMap) Callback(f func(key, value any) bool) {
	//Insert all values to new map
	m.Range(f)
}

func (m *UIntsMap) Clear() {
	//Insert all values to new map
	m.Range(func(key any, value any) bool {
		k := key.(string)
		m.Delete(k)
		return true
	})
}

func NewUIntsMap() *UIntsMap {
	s := new(sync.Map)
	m := &UIntsMap{Map: s}
	return m
}

type PFSQueriesMap struct {
	*sync.Map
}

func NewPFSQueriesMap() *PFSQueriesMap {
	s := new(sync.Map)
	m := &PFSQueriesMap{Map: s}
	return m
}

func (m *PFSQueriesMap) Get(key string) *dbhelper.PFSQuery {
	if v, ok := m.Load(key); ok {
		return v.(*dbhelper.PFSQuery)
	}
	return nil
}

func (m *PFSQueriesMap) CheckAndGet(key string) (*dbhelper.PFSQuery, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*dbhelper.PFSQuery), true
	}
	return nil, false
}

func (m *PFSQueriesMap) Set(key string, value *dbhelper.PFSQuery) {
	m.Store(key, value)
}

func (m *PFSQueriesMap) ToNormalMap(c map[string]*dbhelper.PFSQuery) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the PFSQueriesMap to the output map
	m.Callback(func(key string, value *dbhelper.PFSQuery) bool {
		c[key] = value
		return true
	})
}

func (m *PFSQueriesMap) ToNewMap() map[string]*dbhelper.PFSQuery {
	result := make(map[string]*dbhelper.PFSQuery)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*dbhelper.PFSQuery)
		return true
	})
	return result
}

func (m *PFSQueriesMap) Callback(f func(key string, value *dbhelper.PFSQuery) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*dbhelper.PFSQuery))
	})
}

func (m *PFSQueriesMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalPFSMap(m *PFSQueriesMap, c map[string]dbhelper.PFSQuery) *PFSQueriesMap {
	if m == nil {
		m = NewPFSQueriesMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, &v)
	}

	return m
}

func FromPFSQueriesMap(m *PFSQueriesMap, c *PFSQueriesMap) *PFSQueriesMap {
	if m == nil {
		m = NewPFSQueriesMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *dbhelper.PFSQuery) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type PluginsMap struct {
	*sync.Map
}

func NewPluginsMap() *PluginsMap {
	s := new(sync.Map)
	m := &PluginsMap{Map: s}
	return m
}

func (m *PluginsMap) Get(key string) *dbhelper.Plugin {
	if v, ok := m.Load(key); ok {
		return v.(*dbhelper.Plugin)
	}
	return nil
}

func (m *PluginsMap) CheckAndGet(key string) (*dbhelper.Plugin, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*dbhelper.Plugin), true
	}
	return nil, false
}

func (m *PluginsMap) Set(key string, value *dbhelper.Plugin) {
	m.Store(key, value)
}

func (m *PluginsMap) ToNormalMap(c map[string]*dbhelper.Plugin) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the PluginsMap to the output map
	m.Callback(func(key string, value *dbhelper.Plugin) bool {
		c[key] = value
		return true
	})
}

func (m *PluginsMap) ToNewMap() map[string]*dbhelper.Plugin {
	result := make(map[string]*dbhelper.Plugin)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*dbhelper.Plugin)
		return true
	})
	return result
}

func (m *PluginsMap) Callback(f func(key string, value *dbhelper.Plugin) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*dbhelper.Plugin))
	})
}

func (m *PluginsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalPluginsMap(m *PluginsMap, c map[string]*dbhelper.Plugin) *PluginsMap {
	if m == nil {
		m = NewPluginsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromPluginsMap(m *PluginsMap, c *PluginsMap) *PluginsMap {
	if m == nil {
		m = NewPluginsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *dbhelper.Plugin) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type GrantsMap struct {
	*sync.Map
}

func NewGrantsMap() *GrantsMap {
	s := new(sync.Map)
	m := &GrantsMap{Map: s}
	return m
}

func (m *GrantsMap) Get(key string) *dbhelper.Grant {
	if v, ok := m.Load(key); ok {
		return v.(*dbhelper.Grant)
	}
	return nil
}

func (m *GrantsMap) CheckAndGet(key string) (*dbhelper.Grant, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*dbhelper.Grant), true
	}
	return nil, false
}

func (m *GrantsMap) Set(key string, value *dbhelper.Grant) {
	m.Store(key, value)
}

func (m *GrantsMap) ToNormalMap(c map[string]*dbhelper.Grant) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the GrantsMap to the output map
	m.Callback(func(key string, value *dbhelper.Grant) bool {
		c[key] = value
		return true
	})
}

func (m *GrantsMap) ToNewMap() map[string]*dbhelper.Grant {
	result := make(map[string]*dbhelper.Grant)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*dbhelper.Grant)
		return true
	})
	return result
}

func (m *GrantsMap) Callback(f func(key string, value *dbhelper.Grant) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*dbhelper.Grant))
	})
}

func (m *GrantsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalGrantsMap(m *GrantsMap, c map[string]*dbhelper.Grant) *GrantsMap {
	if m == nil {
		m = NewGrantsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromGrantsMap(m *GrantsMap, c *GrantsMap) *GrantsMap {
	if m == nil {
		m = NewGrantsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *dbhelper.Grant) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type TablesMap struct {
	*sync.Map
}

func (m *TablesMap) Get(key string) *v3.Table {
	if v, ok := m.Load(key); ok {
		return v.(*v3.Table)
	}
	return nil
}

func (m *TablesMap) CheckAndGet(key string) (*v3.Table, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*v3.Table), true
	}
	return nil, false
}

func (m *TablesMap) ToNormalMap(c map[string]*v3.Table) {
	// clear old value
	c = make(map[string]*v3.Table)

	// Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(*v3.Table)
		return true
	})
}

func (m *TablesMap) ToNewMap() map[string]*v3.Table {
	// clear old value
	c := make(map[string]*v3.Table)

	// Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(*v3.Table)
		return true
	})

	return c
}

func (m *TablesMap) Set(k string, v *v3.Table) {
	m.Store(k, v)
}

func FromNormalTablesMap(m *TablesMap, c map[string]*v3.Table) *TablesMap {
	if m == nil {
		m = NewTablesMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Store(k, v)
	}

	return m
}

func FromTablesSyncMap(m *TablesMap, c *TablesMap) *TablesMap {
	if m == nil {
		m = NewTablesMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Range(func(k any, v any) bool {
			m.Store(k.(string), v.(*v3.Table))
			return true
		})
	}

	return m
}

func (m *TablesMap) Callback(f func(key, value any) bool) {
	m.Range(f)
}

func (m *TablesMap) Clear() {
	m.Range(func(key any, value any) bool {
		k := key.(string)
		m.Delete(k)
		return true
	})
}

func NewTablesMap() *TablesMap {
	s := new(sync.Map)
	m := &TablesMap{Map: s}
	return m
}

type WorkLoadsMap struct {
	*sync.Map
}

func NewWorkLoadsMap() *WorkLoadsMap {
	s := new(sync.Map)
	m := &WorkLoadsMap{Map: s}
	return m
}

func (m *WorkLoadsMap) Get(key string) *WorkLoad {
	if v, ok := m.Load(key); ok {
		return v.(*WorkLoad)
	}
	return nil
}

func (m *WorkLoadsMap) GetOrNew(key string) *WorkLoad {
	if v, ok := m.Load(key); ok {
		return v.(*WorkLoad)
	}
	return new(WorkLoad)
}

func (m *WorkLoadsMap) CheckAndGet(key string) (*WorkLoad, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*WorkLoad), true
	}
	return nil, false
}

func (m *WorkLoadsMap) Set(key string, value *WorkLoad) {
	m.Store(key, value)
}

func (m *WorkLoadsMap) ToNormalMap(c map[string]*WorkLoad) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the WorkLoadsMap to the output map
	m.Callback(func(key string, value *WorkLoad) bool {
		c[key] = value
		return true
	})
}

func (m *WorkLoadsMap) ToNewMap() map[string]*WorkLoad {
	result := make(map[string]*WorkLoad)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*WorkLoad)
		return true
	})
	return result
}

func (m *WorkLoadsMap) Callback(f func(key string, value *WorkLoad) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*WorkLoad))
	})
}

func (m *WorkLoadsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalWorkLoadsMap(m *WorkLoadsMap, c map[string]*WorkLoad) *WorkLoadsMap {
	if m == nil {
		m = NewWorkLoadsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromWorkLoadsMap(m *WorkLoadsMap, c *WorkLoadsMap) *WorkLoadsMap {
	if m == nil {
		m = NewWorkLoadsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *WorkLoad) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type TasksMap struct {
	*sync.Map
}

func NewTasksMap() *TasksMap {
	s := new(sync.Map)
	m := &TasksMap{Map: s}
	return m
}

func (m *TasksMap) Get(key string) *Task {
	if v, ok := m.Load(key); ok {
		return v.(*Task)
	}
	return nil
}

func (m *TasksMap) CheckAndGet(key string) (*Task, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*Task), true
	}
	return nil, false
}

func (m *TasksMap) Set(key string, value *Task) {
	m.Store(key, value)
}

func (m *TasksMap) LoadOrStore(key string, value *Task) (*Task, bool) {
	v, ok := m.Map.LoadOrStore(key, value)
	return v.(*Task), ok
}

func (m *TasksMap) ToNormalMap(c map[string]*Task) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the TasksMap to the output map
	m.Callback(func(key string, value *Task) bool {
		c[key] = value
		return true
	})
}

func (m *TasksMap) ToNewMap() map[string]*Task {
	result := make(map[string]*Task)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*Task)
		return true
	})
	return result
}

func (m *TasksMap) Callback(f func(key string, value *Task) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*Task))
	})
}

func (m *TasksMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalTasksMap(m *TasksMap, c map[string]*Task) *TasksMap {
	if m == nil {
		m = NewTasksMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromTasksMap(m *TasksMap, c *TasksMap) *TasksMap {
	if m == nil {
		m = NewTasksMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *Task) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
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
			if backup.BackupTool == backupTool && backup.Source == source {
				result = backup
				return false
			}
		}
		return true
	})
	return result
}

type VersionsMap struct {
	*sync.Map
}

func NewVersionsMap() *VersionsMap {
	s := new(sync.Map)
	m := &VersionsMap{Map: s}
	return m
}

func (m *VersionsMap) Get(key string) *version.Version {
	if v, ok := m.Load(key); ok {
		return v.(*version.Version)
	}
	return nil
}

func (m *VersionsMap) CheckAndGet(key string) (*version.Version, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*version.Version), true
	}
	return nil, false
}

func (m *VersionsMap) Set(key string, value *version.Version) {
	m.Store(key, value)
}

func (m *VersionsMap) ToNormalMap(c map[string]*version.Version) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the VersionsMap to the output map
	m.Callback(func(key string, value *version.Version) bool {
		c[key] = value
		return true
	})
}

func (m *VersionsMap) ToNewMap() map[string]*version.Version {
	result := make(map[string]*version.Version)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*version.Version)
		return true
	})
	return result
}

func (m *VersionsMap) Callback(f func(key string, value *version.Version) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*version.Version))
	})
}

func (m *VersionsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalVersionsMap(m *VersionsMap, c map[string]*version.Version) *VersionsMap {
	if m == nil {
		m = NewVersionsMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromVersionsMap(m *VersionsMap, c *VersionsMap) *VersionsMap {
	if m == nil {
		m = NewVersionsMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *version.Version) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}
