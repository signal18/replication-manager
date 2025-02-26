package clusterauth

import (
	"slices"
	"strings"
)

// GetGrantType returns a map of all grants for quick lookup
func GetGrantType() map[string]bool {
	grantSet := make(map[string]bool, len(allGrants))
	for _, grant := range allGrants {
		grantSet[grant] = true
	}
	return grantSet
}

// Generic function to check if all required grants exist in a given set
func HasAllGrants(grants map[string]bool, required []string) bool {
	for _, grant := range required {
		if !grants[grant] {
			return false
		}
	}
	return true
}

// Functions to return specific grant categories
func GetGrantDB() []string        { return grantDB }
func GetGrantCluster() []string   { return grantCluster }
func GetGrantProxy() []string     { return grantProxy }
func GetGrantProvision() []string { return grantProvision }
func GetGrantGlobal() []string    { return grantGlobal }
func GetGrantSales() []string     { return grantSales }
func GetGrantGrant() []string     { return grantGrant }

// Wrapper functions for grant checks using HasAllGrants
func HasAllDBGrants(grants map[string]bool) bool        { return HasAllGrants(grants, grantDB) }
func HasAllClusterGrants(grants map[string]bool) bool   { return HasAllGrants(grants, grantCluster) }
func HasAllProxyGrants(grants map[string]bool) bool     { return HasAllGrants(grants, grantProxy) }
func HasAllProvisionGrants(grants map[string]bool) bool { return HasAllGrants(grants, grantProvision) }
func HasAllGlobalGrants(grants map[string]bool) bool    { return HasAllGrants(grants, grantGlobal) }
func HasAllSalesGrants(grants map[string]bool) bool     { return HasAllGrants(grants, grantSales) }
func HasAllGrantGrants(grants map[string]bool) bool     { return HasAllGrants(grants, grantGrant) }

// Define grant categories and their compact names
var grantCategories = []struct {
	name   string
	grants []string
	hasAll func(map[string]bool) bool
}{
	{"db", grantDB, HasAllDBGrants},
	{"cluster", grantCluster, HasAllClusterGrants},
	{"proxy", grantProxy, HasAllProxyGrants},
	{"prov", grantProvision, HasAllProvisionGrants},
	{"global", grantGlobal, HasAllGlobalGrants},
	{"sales", grantSales, HasAllSalesGrants},
	{"grant", grantGrant, HasAllGrantGrants},
}

func GetCompactGrants(grants map[string]bool) ([]string, []string) {
	var compactGrants, compactDiscardGrants []string

	// Iterate through all categories dynamically
	for _, category := range grantCategories {
		if category.hasAll(grants) {
			compactGrants = append(compactGrants, category.name)
		} else {
			tmp := make([]string, 0)
			counter := 0
			for _, grant := range category.grants {
				if grants[grant] {
					compactGrants = append(compactGrants, grant)
					counter++
				} else {
					tmp = append(tmp, grant)
				}
			}
			if counter == 0 {
				compactDiscardGrants = append(compactDiscardGrants, category.name)
			} else {
				compactDiscardGrants = append(compactDiscardGrants, tmp...)
			}
		}
	}

	// Handle standalone grants separately
	standaloneGrants := map[string]string{
		"show":    "show",
		"extrole": "extrole",
	}

	for grant, name := range standaloneGrants {
		if grants[grant] {
			compactGrants = append(compactGrants, name)
		} else {
			compactDiscardGrants = append(compactDiscardGrants, name)
		}
	}

	return compactGrants, compactDiscardGrants
}

// GetRoleType returns all available roles
func GetRoleType() []string {
	return allRoles
}

// GetCompactRoles extracts role names from a map where the value is true
func GetCompactRoles(roles map[string]bool) []string {
	var roleList []string
	for role, enabled := range roles {
		if enabled {
			roleList = append(roleList, role)
		}
	}
	return roleList
}

// GetDefaultAllowDiscardACL returns allowed and discarded grants for a role
func GetDefaultAllowDiscardACL(role string) (allow, discard string) {
	roleACL := map[string]struct {
		allow, discard string
	}{
		RoleSysOps:    {"*", ""},
		RoleExtSysOps: {"*", "sales global extrole"},
		RoleDBOps:     {"*", "cluster prov sales global"},
		RoleSponsor:   {"db show proxy grant extrole sales-unsubscribe", ""},
		RoleExtDBOps:  {"db show proxy grant", "extrole"},
	}

	if acl, exists := roleACL[role]; exists {
		return acl.allow, acl.discard
	}
	return "show", ""
}

// GetDefaultGrants returns a space-separated list of default grants for a role
func GetDefaultGrants(role string) string {
	grants := make(map[string]bool)
	allow, discard := GetDefaultAllowDiscardACL(role)

	allowList := strings.Fields(allow)
	discardList := strings.Fields(discard)

	for _, grant := range allGrants {
		// Check if grant is allowed
		allowed := allow == "*" || matchesPrefix(grant, allowList)

		// Remove grant if it's in the discard list
		if allowed && matchesPrefix(grant, discardList) {
			allowed = false
		}

		grants[grant] = allowed
	}

	compactGrants, _ := GetCompactGrants(grants)
	return strings.Join(compactGrants, " ")
}

// GetRoleDefaultGrant returns default grants for a given role
func GetRoleDefaultGrant(roles string) string {
	roleList := strings.Fields(roles)

	// Prioritize roles based on hierarchy
	rolePriority := []string{
		RoleSysOps, RoleExtSysOps, RoleSponsor, RoleDBOps, RoleExtDBOps,
	}

	for _, role := range rolePriority {
		if slices.Contains(roleList, role) {
			return GetDefaultGrants(role)
		}
	}
	return ""
}

// matchesPrefix checks if a grant starts with any prefix from a list
func matchesPrefix(grant string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(grant, prefix) {
			return true
		}
	}
	return false
}
