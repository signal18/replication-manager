package user

import "sync"

type UserCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type User struct {
	User     string   `json:"user"`
	Password string   `json:"-"`
	GitToken string   `json:"-"`
	GitUser  string   `json:"-"`
	Grants   sync.Map `json:"grants"`
}

type UserToken struct {
	User  *User  `json:"user"`
	Token string `json:"token"`
}

// SetClusterPermissions bulk sets cluster-level permissions for the user
func (u *User) SetClusterPermissions(clusterID string, permissions []string, allowed bool) {
	clusterPerms, _ := u.Grants.LoadOrStore(clusterID, &sync.Map{})
	for _, permission := range permissions {
		clusterPerms.(*sync.Map).Store(permission, allowed)
	}
}

// HasClusterPermission checks if the user has a cluster-level permission.
func (u *User) HasClusterPermission(clusterID, permission string) bool {
	if clusterPerms, ok := u.Grants.Load(clusterID); ok {
		value, ok := clusterPerms.(*sync.Map).Load(permission)
		if ok {
			return value.(bool)
		}
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
