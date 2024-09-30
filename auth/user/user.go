package user

import (
	"strings"
	"sync"
)

type UserCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UserForm struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Clusters string `json:"clusters"`
	Grants   string `json:"grants"`
}

type User struct {
	User     string                    `json:"user"`
	Password string                    `json:"-"`
	GitToken string                    `json:"-"`
	GitUser  string                    `json:"-"`
	GrantMap map[string]*ClusterGrants `json:"grantMap"`
	mu       sync.RWMutex              `json:"-"`
	WG       sync.WaitGroup            `json:"-"`
}

type ClusterGrants struct {
	Role   string          `json:"role"`
	Grants map[string]bool `json:"grants"`
}

// NewClusterGrants initializes a new ClusterGrants instance.
func NewClusterGrants() *ClusterGrants {
	return &ClusterGrants{
		Grants: make(map[string]bool),
	}
}

type UserToken struct {
	User  *User  `json:"user"`
	Token string `json:"token"`
}

func NewUser(username, password string) *User {
	return &User{
		User:     username,
		Password: password,
		GrantMap: make(map[string]*ClusterGrants),
	}
}

// SetClusterPermissions bulk sets permissions for the user.
func (u *User) SetClusterPermissions(clusterID string, permissions []string, allowed bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	cGrant, exists := u.GrantMap[clusterID]
	if !exists {
		cGrant = NewClusterGrants()
		u.GrantMap[clusterID] = cGrant
	}

	for _, permission := range permissions {
		cGrant.Grants[permission] = allowed
	}
}

// SetClusterRole sets the role for a user in a specified cluster.
func (u *User) SetClusterRole(clusterID string, role string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	cGrants, exists := u.GrantMap[clusterID]
	if !exists {
		cGrants = NewClusterGrants()
		u.GrantMap[clusterID] = cGrants
	}
	cGrants.Role = role
}

// HasClusterPermission checks if the user has a specific permission.
func (u *User) HasClusterPermission(clusterID, permission string) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()

	cGrants, ok := u.GrantMap[clusterID]
	if ok {
		for key, value := range cGrants.Grants {
			if strings.HasPrefix(permission, key) {
				return value // Return the value (true or false) for the permission.
			}
		}
	}
	return false
}
