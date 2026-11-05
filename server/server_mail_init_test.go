// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

// The restart mail obeys the mute: every cluster under an intervention (or the scheduler's alert
// disable), or a global intervention, and it is not sent; one cluster not muted and it goes. The
// skipped mail is counted on each cluster under an intervention; a cluster muted by the alert
// disable alone has no counter. A nil entry is never touched.
func TestInitMailMuted(t *testing.T) {
	a, b := &cluster.Cluster{}, &cluster.Cluster{}
	repman := &ReplicationManager{Conf: &config.Config{MailTo: "ops@x.io"}, Logrus: log.New(),
		Clusters: map[string]*cluster.Cluster{"a": a, "b": b}}

	if repman.initMailMuted() {
		t.Fatal("no mute: the restart mail goes")
	}
	a.IsIntervention = true
	if repman.initMailMuted() || a.InterventionSuppressedAlerts != 0 {
		t.Fatalf("one cluster not muted: the restart mail goes, nothing counted (%d)", a.InterventionSuppressedAlerts)
	}
	b.IsAlertDisable = true
	// no SMTP set: the mute is checked before the mailer config, so the send stops at the mute
	repman.SendClustersInitMail()
	if a.InterventionSuppressedAlerts != 1 || b.InterventionSuppressedAlerts != 0 {
		t.Fatalf("every cluster muted: no restart mail, counted on the intervention only (a %d, b %d)",
			a.InterventionSuppressedAlerts, b.InterventionSuppressedAlerts)
	}

	a.IsIntervention, b.IsAlertDisable = false, false
	repman.IsGlobalIntervention = true
	repman.Clusters["nil"] = nil
	if !repman.initMailMuted() {
		t.Fatal("a global intervention mutes the restart mail")
	}
	if (&ReplicationManager{Conf: &config.Config{}}).initMailMuted() {
		t.Fatal("no cluster, no intervention: not muted")
	}
}
