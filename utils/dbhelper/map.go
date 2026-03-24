// This file contains thread-safe concurrent map wrappers for binlog and query tracking.
// It provides synchronized data structures for managing binary log metadata
// and Performance Schema query statistics across multiple goroutines.

package dbhelper

import "sync"

type PFSQueriesMap struct {
	*sync.Map
}

func NewPFSQueriesMap() *PFSQueriesMap {
	s := new(sync.Map)
	m := &PFSQueriesMap{Map: s}
	return m
}

func (m *PFSQueriesMap) Get(key string) *PFSQuery {
	if v, ok := m.Load(key); ok {
		return v.(*PFSQuery)
	}
	return nil
}

func (m *PFSQueriesMap) CheckAndGet(key string) (*PFSQuery, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*PFSQuery), true
	}
	return nil, false
}

func (m *PFSQueriesMap) Set(key string, value *PFSQuery) {
	m.Store(key, value)
}

func (m *PFSQueriesMap) ToNormalMap(c map[string]*PFSQuery) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the PFSQueriesMap to the output map
	m.Callback(func(key string, value *PFSQuery) bool {
		c[key] = value
		return true
	})
}

func (m *PFSQueriesMap) ToNewMap() map[string]*PFSQuery {
	result := make(map[string]*PFSQuery)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*PFSQuery)
		return true
	})
	return result
}

func (m *PFSQueriesMap) Callback(f func(key string, value *PFSQuery) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*PFSQuery))
	})
}

func (m *PFSQueriesMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalPFSMap(m *PFSQueriesMap, c map[string]PFSQuery) *PFSQueriesMap {
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
		c.Callback(func(key string, value *PFSQuery) bool {
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

func (m *PluginsMap) Get(key string) *Plugin {
	if v, ok := m.Load(key); ok {
		return v.(*Plugin)
	}
	return nil
}

func (m *PluginsMap) CheckAndGet(key string) (*Plugin, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*Plugin), true
	}
	return nil, false
}

func (m *PluginsMap) Set(key string, value *Plugin) {
	m.Store(key, value)
}

func (m *PluginsMap) ToNormalMap(c map[string]*Plugin) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the PluginsMap to the output map
	m.Callback(func(key string, value *Plugin) bool {
		c[key] = value
		return true
	})
}

func (m *PluginsMap) ToNewMap() map[string]*Plugin {
	result := make(map[string]*Plugin)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*Plugin)
		return true
	})
	return result
}

func (m *PluginsMap) Callback(f func(key string, value *Plugin) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*Plugin))
	})
}

func (m *PluginsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalPluginsMap(m *PluginsMap, c map[string]*Plugin) *PluginsMap {
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
		c.Callback(func(key string, value *Plugin) bool {
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

func (m *GrantsMap) Get(key string) *Grant {
	if v, ok := m.Load(key); ok {
		return v.(*Grant)
	}
	return nil
}

func (m *GrantsMap) CheckAndGet(key string) (*Grant, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*Grant), true
	}
	return nil, false
}

func (m *GrantsMap) Set(key string, value *Grant) {
	m.Store(key, value)
}

func (m *GrantsMap) ToNormalMap(c map[string]*Grant) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the GrantsMap to the output map
	m.Callback(func(key string, value *Grant) bool {
		c[key] = value
		return true
	})
}

func (m *GrantsMap) ToNewMap() map[string]*Grant {
	result := make(map[string]*Grant)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*Grant)
		return true
	})
	return result
}

func (m *GrantsMap) Callback(f func(key string, value *Grant) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*Grant))
	})
}

func (m *GrantsMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalGrantsMap(m *GrantsMap, c map[string]*Grant) *GrantsMap {
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
		c.Callback(func(key string, value *Grant) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type TablesMap struct {
	*sync.Map
}

func (m *TablesMap) Get(key string) *Table {
	if v, ok := m.Load(key); ok {
		return v.(*Table)
	}
	return nil
}

func (m *TablesMap) CheckAndGet(key string) (*Table, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*Table), true
	}
	return nil, false
}

func (m *TablesMap) ToNormalMap(c map[string]*Table) {
	// clear old value
	c = make(map[string]*Table)

	// Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(*Table)
		return true
	})
}

func (m *TablesMap) ToNewMap() map[string]*Table {
	// clear old value
	c := make(map[string]*Table)

	// Insert all values to new map
	m.Range(func(k any, v any) bool {
		c[k.(string)] = v.(*Table)
		return true
	})

	return c
}

func (m *TablesMap) Set(k string, v *Table) {
	m.Store(k, v)
}

func FromNormalTablesMap(m *TablesMap, c map[string]*Table) *TablesMap {
	if m == nil {
		m = NewTablesMap()
	} else {
		// Remove keys that no longer exist in the new schema scan
		m.Range(func(k any, v any) bool {
			if _, exists := c[k.(string)]; !exists {
				m.Delete(k)
			}
			return true
		})
	}

	for k, v := range c {
		// Preserve checksum runtime state from the existing entry
		if existing, ok := m.Load(k); ok {
			old := existing.(*Table)
			v.TableSync = old.TableSync
			v.TableChunksError = old.TableChunksError
			v.TableChunksCount = old.TableChunksCount
			v.TableChunksCurrent = old.TableChunksCurrent
		}
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
			m.Store(k.(string), v.(*Table))
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
