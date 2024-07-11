package config

import (
	"sync"

	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/dbhelper"
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
