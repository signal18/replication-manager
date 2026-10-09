// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package cluster

import (
	"errors"
	"sync"

	"github.com/signal18/replication-manager/config"
)

// DR fallback of the OpenSVC init containers (#1942).
//
// An init container fetches its bootstrap script and configuration from
// env/REPLICATION_MANAGER_URL, the instance that provisioned the service; only
// provisioning rewrites it. With active/standby enabled (arbitration-external),
// the instance writing the definition puts the API URLs of the pair -- its own,
// then its peer's, learned from the peer's heartbeat answer -- in the same
// namespace config as REPLICATION_MANAGER_URL_DR, mapped into the init
// container. The init container and the script try REPLICATION_MANAGER_URL,
// then each URL of the list not tried yet, so whichever instance is alive
// answers, whichever of the two wrote the keys.

// bootstrapDRURLKey is the key of the namespace config `env` holding the DR URL.
const bootstrapDRURLKey = "REPLICATION_MANAGER_URL_DR"

type bootstrapDRState struct {
	mu      sync.Mutex
	url     string // the pair's API URLs (own, then peer), empty without active/standby
	written string // the value last written to the namespace config
	// writer replaces the orchestrator write in tests.
	writer func(key, value string) error
}

// SetBootstrapDRURL sets the DR URL offered to init containers; empty when
// active/standby is disabled or the peer's URL is not known yet.
func (cluster *Cluster) SetBootstrapDRURL(url string) {
	cluster.bootstrapDR.mu.Lock()
	cluster.bootstrapDR.url = url
	cluster.bootstrapDR.mu.Unlock()
}

// bootstrapDRURLs is the pair's URLs (own, then peer) offered to bootstrap
// scripts, empty without active/standby. On-premise exports it over SSH and
// Kubernetes bakes it into the init container command; OpenSVC goes through
// the namespace config (bootstrapDRURLMapped).
func (cluster *Cluster) bootstrapDRURLs() string {
	cluster.bootstrapDR.mu.Lock()
	defer cluster.bootstrapDR.mu.Unlock()
	return cluster.bootstrapDR.url
}

// onPremiseBootstrapCommand fetches an on-premise bootstrap script from
// REPLICATION_MANAGER_URL, then from each REPLICATION_MANAGER_URL_DR URL not
// tried yet, and runs it with the URL that answered.
func onPremiseBootstrapCommand(path string) string {
	return `tried=; for u in $REPLICATION_MANAGER_URL $REPLICATION_MANAGER_URL_DR; do case " $tried " in *" $u "*) continue;; esac; tried="$tried $u"; rm -f /tmp/replication-manager-bootstrap; ` +
		`if wget --no-check-certificate -q -T 10 -O /tmp/replication-manager-bootstrap $u/static/configurator/onpremise/` + path + `/bootstrap; then ` +
		`REPLICATION_MANAGER_URL=$u sh /tmp/replication-manager-bootstrap; exit $?; fi; echo "bootstrap: $u did not answer" >&2; done; exit 1`
}

// bootstrapDRURLMapped reports whether the init container may map the DR URL:
// the key must exist in the namespace config, since a mapped key that does not
// exist fails the start. It is written once per value; a failed write keeps it
// unmapped until the next render.
func (cluster *Cluster) bootstrapDRURLMapped() bool {
	cluster.bootstrapDR.mu.Lock()
	url, written := cluster.bootstrapDR.url, cluster.bootstrapDR.written
	cluster.bootstrapDR.mu.Unlock()
	if url == "" {
		return false
	}
	if url == written {
		return true
	}
	if err := cluster.writeBootstrapDRURL(url); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"DR replication-manager URL %s not written to namespace %s (%s): init containers keep the main URL only", url, cluster.Name, err)
		return false
	}
	cluster.bootstrapDR.mu.Lock()
	cluster.bootstrapDR.written = url
	cluster.bootstrapDR.mu.Unlock()
	return true
}

func (cluster *Cluster) writeBootstrapDRURL(url string) error {
	if cluster.bootstrapDR.writer != nil {
		return cluster.bootstrapDR.writer(bootstrapDRURLKey, url)
	}
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI {
		return errors.New("OpenSVC cluster API only")
	}
	svc := cluster.OpenSVCConnect()
	if svc.IsV3() {
		return svc.CreateConfigKeyValue(cluster.Name, "env", bootstrapDRURLKey, url)
	}
	return svc.CreateConfigKeyValueV2(cluster.Name, "env", bootstrapDRURLKey, url)
}

// bootstrapInitCommand fetches the bootstrap script from the main URL, then from
// each DR URL not tried yet, and runs it with the URL that answered. busybox
// wget: -T bounds each read, there is no retry count option.
const bootstrapInitCommand = `-c 'tried=; for u in $REPLICATION_MANAGER_URL $REPLICATION_MANAGER_URL_DR; do case " $tried " in *" $u "*) continue;; esac; tried="$tried $u"; rm -f /tmp/bootstrap; wget --no-check-certificate -q -T 10 -O /tmp/bootstrap $u/static/configurator/opensvc/bootstrap && REPLICATION_MANAGER_URL=$u exec sh /tmp/bootstrap; echo "bootstrap: $u did not answer" >&2; done; exit 1'`
