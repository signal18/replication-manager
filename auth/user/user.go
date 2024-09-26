package user

import (
	"strings"
	"sync"
)

type UserCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type User struct {
	User     string          `json:"user"`
	Password string          `json:"-"`
	GitToken string          `json:"-"`
	GitUser  string          `json:"-"`
	Grants   *ServerGrantMap `json:"-"`
}

type UserToken struct {
	User  *User  `json:"user"`
	Token string `json:"token"`
}

func NewUser() *User {
	return &User{
		Grants: NewServerGrantMap(),
	}
}

// SetClusterPermissions bulk sets permissions for the user
func (u *User) SetClusterPermissions(clusterID string, permissions []string, allowed bool) {
	cl, _ := u.Grants.LoadOrStore(clusterID, NewGrantMap())
	clusterPerms := cl.(*GrantMap)
	for _, permission := range permissions {
		clusterPerms.Set(permission, allowed)
	}
}

// HasClusterPermission checks if the user has a permission.
func (u *User) HasClusterPermission(clusterID, permission string) bool {
	if clusterPerms, ok := u.Grants.CheckAndGet(clusterID); ok {
		valid := false
		clusterPerms.Callback(func(key string, value bool) bool {
			if strings.HasPrefix(permission, key) {
				valid = true
				return false
			}
			return true
		})
		return valid
	}
	return false
}

type UserMap struct {
	*sync.Map
}

func NewUserMap() *UserMap {
	s := new(sync.Map)
	m := &UserMap{Map: s}
	return m
}

func (m *UserMap) Get(key string) *User {
	if v, ok := m.Load(key); ok {
		return v.(*User)
	}
	return nil
}

func (m *UserMap) CheckAndGet(key string) (*User, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*User), true
	}
	return nil, false
}

func (m *UserMap) Set(key string, value *User) {
	m.Store(key, value)
}

func (m *UserMap) ToNormalMap(c map[string]*User) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the UserMap to the output map
	m.Callback(func(key string, value *User) bool {
		c[key] = value
		return true
	})
}

func (m *UserMap) ToNewMap() map[string]*User {
	result := make(map[string]*User)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*User)
		return true
	})
	return result
}

func (m *UserMap) Callback(f func(key string, value *User) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*User))
	})
}

func (m *UserMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalUserMap(m *UserMap, c map[string]*User) *UserMap {
	if m == nil {
		m = NewUserMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromUserMap(m *UserMap, c *UserMap) *UserMap {
	if m == nil {
		m = NewUserMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *User) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type GrantMap struct {
	*sync.Map
}

func NewGrantMap() *GrantMap {
	s := new(sync.Map)
	m := &GrantMap{Map: s}
	return m
}

func (m *GrantMap) Get(key string) bool {
	v, ok := m.Load(key)
	if ok {
		return v.(bool)
	}
	return false
}

func (m *GrantMap) Set(key string, value bool) {
	m.Store(key, value)
}

func (m *GrantMap) ToNormalMap(c map[string]bool) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the GrantMap to the output map
	m.Callback(func(key string, value bool) bool {
		c[key] = value
		return true
	})
}

func (m *GrantMap) ToNewMap() map[string]bool {
	result := make(map[string]bool)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(bool)
		return true
	})
	return result
}

func (m *GrantMap) Callback(f func(key string, value bool) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(bool))
	})
}

func (m *GrantMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalGrantMap(m *GrantMap, c map[string]bool) *GrantMap {
	if m == nil {
		m = NewGrantMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromGrantMap(m *GrantMap, c *GrantMap) *GrantMap {
	if m == nil {
		m = NewGrantMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value bool) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

type ServerGrantMap struct {
	*sync.Map
}

func NewServerGrantMap() *ServerGrantMap {
	s := new(sync.Map)
	m := &ServerGrantMap{Map: s}
	return m
}

func (m *ServerGrantMap) Get(key string) *GrantMap {
	if v, ok := m.Load(key); ok {
		return v.(*GrantMap)
	}
	return nil
}

func (m *ServerGrantMap) CheckAndGet(key string) (*GrantMap, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*GrantMap), true
	}
	return nil, false
}

func (m *ServerGrantMap) Set(key string, value *GrantMap) {
	m.Store(key, value)
}

func (m *ServerGrantMap) ToNormalMap(c map[string]*GrantMap) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the GrantMap to the output map
	m.Callback(func(key string, value *GrantMap) bool {
		c[key] = value
		return true
	})
}

func (m *ServerGrantMap) ToNewMap() map[string]*GrantMap {
	result := make(map[string]*GrantMap)
	m.Range(func(k, v any) bool {
		result[k.(string)] = v.(*GrantMap)
		return true
	})
	return result
}

func (m *ServerGrantMap) Callback(f func(key string, value *GrantMap) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*GrantMap))
	})
}

func (m *ServerGrantMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

func FromNormalServerGrantMap(m *ServerGrantMap, c map[string]*GrantMap) *ServerGrantMap {
	if m == nil {
		m = NewServerGrantMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromServerGrantMap(m *ServerGrantMap, c *ServerGrantMap) *ServerGrantMap {
	if m == nil {
		m = NewServerGrantMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *GrantMap) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}
