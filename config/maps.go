package config

import "sync"

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

func FromNormalMap(m *StringsMap, c map[string]string) *StringsMap {
	if m == nil {
		m = NewStringsMap()
	}
	m.Clear()
	if c == nil {
		c = make(map[string]string)
	}
	for k, v := range c {
		m.Store(k, v)
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
