// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"net/url"
	"time"
)

// GetStateRemediationPlan is the remediation of the operational states that have one: a backup
// directory put back to a snapshot (WARN0255) or a snapback that failed (WARN0256) is left by a
// snapback to its latest snapshot (today's directory) or by a release (the directory kept as it
// is, its next backup's snapshot the new latest). Each fix is a server_action the alerts call.
func (cluster *Cluster) GetStateRemediationPlan() RemediationPlan {
	plan := RemediationPlan{Cluster: cluster.Name, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Remediations: make([]RemediationEntry, 0)}
	for _, s := range cluster.Snapbacks() {
		var key string
		switch {
		case s.Status == SnapbackRunning:
			continue // nothing to do but wait
		case s.Status == SnapbackFailed:
			key = "WARN0256"
		case s.locked():
			key = "WARN0255"
		default:
			continue
		}
		name := s.Server
		if srv := cluster.GetServerFromURL(s.Server); srv != nil && srv.Name != "" {
			name = srv.Name
		}
		var fixes []RemediationFix
		if s.Head != "" {
			fixes = append(fixes, RemediationFix{
				Type:        "server_action",
				Description: fmt.Sprintf("Snapback to the latest snapshot %s: the backup directory back as it was before the snapback, its backups and archive pushes open again", shortID(s.Head)),
				Risk:        "moderate",
				URL:         fmt.Sprintf("clusters/%s/archive-actions/snapback/%s", url.PathEscape(cluster.Name), url.PathEscape(s.Head)),
			})
		}
		fixes = append(fixes, RemediationFix{
			Type:        "server_action",
			Description: "Release: keep the backup directory as it is now and open its backups again; its next backup's snapshot is its new latest",
			Risk:        "safe",
			URL:         fmt.Sprintf("clusters/%s/archive-actions/snapback-release/%s", url.PathEscape(cluster.Name), url.PathEscape(name)),
		})
		plan.Remediations = append(plan.Remediations, RemediationEntry{
			ErrKey:      key,
			Server:      s.Server,
			Description: fmt.Sprintf("Snapback of %s at snapshot %s of archive %s", s.Server, shortID(s.Snapshot), s.Archive),
			AutoFixable: true,
			Fixes:       fixes,
		})
	}
	plan.OpenFindings = len(plan.Remediations)
	return plan
}
