// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2227: an operator (cluster-settings) manages the vault entries; the cluster's sponsor
// (its default grants: db-config-flag) sees them and turns them on or off, never adds, edits
// or removes one
func TestACL_SecretKvEntries(t *testing.T) {
	cl := setupACLTestCluster()
	sponsor := map[string]bool{}
	// the sponsor's default grants, compact ("db" for every db-* grant)
	for _, compact := range strings.Split(config.GetDefaultGrants(config.RoleSponsor), " ") {
		for _, g := range config.GetGrantType() {
			if compact != "" && strings.HasPrefix(g, compact) {
				sponsor[g] = true
			}
		}
	}
	cl.APIUsers["sponsor"] = APIUser{User: "sponsor", Grants: sponsor}
	cl.APIUsers["operator"] = APIUser{User: "operator", Grants: map[string]bool{config.GrantClusterSettings: true}}
	base := "/api/clusters/" + cl.Name
	for path, sponsorOK := range map[string]bool{
		"/secret-kv-list":                                 true,
		"/secret-kv-actions/enable/cloud18":               true,
		"/secret-kv-actions/disable/cloud18":              true,
		"/secret-kv-actions/add":                          false,
		"/secret-kv-actions/edit/cloud18":                 false,
		"/secret-kv-actions/remove/cloud18":               false,
		"/settings/actions/set/secret-kv-start-timeout/5": false,
	} {
		if got := cl.IsURLPassACL("sponsor", base+path, false); got != sponsorOK {
			t.Errorf("sponsor %s: %v, want %v", path, got, sponsorOK)
		}
		if !cl.IsURLPassACL("operator", base+path, false) {
			t.Errorf("operator %s refused", path)
		}
	}
}
