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
// disable), or a global intervention, and it is not sent, counted as suppressed on each muted
// cluster; one cluster not muted and it goes (here: on to the mailer checks).
func TestInitMailMuted(t *testing.T) {
	a, b := &cluster.Cluster{}, &cluster.Cluster{}
	repman := &ReplicationManager{Conf: &config.Config{MailTo: "ops@x.io"}, Logrus: log.New(),
		Clusters: map[string]*cluster.Cluster{"a": a, "b": b}}

	if repman.initMailMuted() {
		t.Fatal("no mute: the restart mail goes")
	}
	a.IsIntervention = true
	if repman.initMailMuted() {
		t.Fatal("one cluster not muted: the restart mail goes")
	}
	b.IsAlertDisable = true
	if !repman.initMailMuted() {
		t.Fatal("every cluster muted (intervention, alert disable): no restart mail")
	}
	repman.SendClustersInitMail()
	if a.InterventionSuppressedAlerts != 1 {
		t.Fatalf("the skipped mail is counted on the intervention: %d", a.InterventionSuppressedAlerts)
	}

	a.IsIntervention, b.IsAlertDisable = false, false
	repman.IsGlobalIntervention = true
	if !repman.initMailMuted() {
		t.Fatal("a global intervention mutes the restart mail")
	}
	if (&ReplicationManager{Conf: &config.Config{}}).initMailMuted() {
		t.Fatal("no cluster, no intervention: not muted")
	}
}
