package auth

import (
	"sync"
)

type AuthTryMap struct {
	*sync.Map
}

func NewAuthTryMap() *AuthTryMap {
	s := new(sync.Map)
	m := &AuthTryMap{Map: s}
	return m
}

func (m *AuthTryMap) Get(key string) *AuthTry {
	if v, ok := m.Load(key); ok {
		return v.(*AuthTry)
	}
	return nil
}

func (m *AuthTryMap) CheckAndGet(key string) (*AuthTry, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*AuthTry), true
	}
	return nil, false
}

func (m *AuthTryMap) Set(key string, value *AuthTry) {
	m.Store(key, value)
}

func (m *AuthTryMap) ToNormalMap(c map[string]*AuthTry) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the AuthTryMap to the output map
	m.Callback(func(key string, value *AuthTry) bool {
		c[key] = value
		return true
	})
}

func (m *AuthTryMap) ToNewMap() map[string]*AuthTry {
	result := make(map[string]*AuthTry)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*AuthTry)
		return true
	})
	return result
}

func (m *AuthTryMap) Callback(f func(key string, value *AuthTry) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*AuthTry))
	})
}

func (m *AuthTryMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalAuthTryMap(m *AuthTryMap, c map[string]*AuthTry) *AuthTryMap {
	if m == nil {
		m = NewAuthTryMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromAuthTryMap(m *AuthTryMap, c *AuthTryMap) *AuthTryMap {
	if m == nil {
		m = NewAuthTryMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *AuthTry) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}
