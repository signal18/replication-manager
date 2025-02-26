package clusterauth

import (
	"strings"
)

// SetUserGrants updates the user's grant permissions based on the provided grant string.
func (u *APIUser) SetUserGrants(grants string) {
	// Initialize Grants map if it's nil
	if u.Grants == nil {
		u.Grants = make(map[string]bool)
	}

	// Convert grant string into a set for fast lookups
	aclSet := make(map[string]struct{})
	for _, acl := range strings.Fields(grants) { // `strings.Fields` trims and splits by spaces
		aclSet[acl] = struct{}{}
	}

	// Iterate through all available grants
	for _, grant := range allGrants {
		_, hasPrefix := aclSet[grant] // Check if the grant itself is explicitly listed
		for acl := range aclSet {
			if strings.HasPrefix(grant, acl) { // Check if grant starts with any allowed ACL
				hasPrefix = true
				break
			}
		}
		// Update the user's grant map
		u.Grants[grant] = hasPrefix
	}
}

// SetUserRoles updates the user's roles based on the provided roles string.
func (u *APIUser) SetUserRoles(roles string) {
	// Initialize Roles map if it's nil
	if u.Roles == nil {
		u.Roles = make(map[string]bool)
	}

	// Convert role string into a set for fast lookups
	roleSet := make(map[string]struct{})
	for _, role := range strings.Fields(roles) { // `strings.Fields` trims and splits by spaces
		roleSet[role] = struct{}{}
	}

	// Iterate through all available roles and update the user's role map
	for _, role := range allRoles {
		_, exists := roleSet[role]
		u.Roles[role] = exists // Assign true if role exists in the provided roles
	}
}

func (u *APIUser) ExportUserRoles(user string) string {
	var aEnabledRoles []string
	for role, value := range u.Roles {
		if value {
			aEnabledRoles = append(aEnabledRoles, role)
		}
	}
	return strings.Join(aEnabledRoles, " ")
}
