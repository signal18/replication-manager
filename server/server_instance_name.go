// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"math/big"
	"strconv"

	"github.com/signal18/replication-manager/config"
)

// instanceZoneMax bounds the random zone of an unregistered instance's name: six digits.
const instanceZoneMax = 1000000

// randomInstanceZone draws the zone of an unregistered instance's name.
func randomInstanceZone() string {
	n, err := rand.Int(rand.Reader, big.NewInt(instanceZoneMax))
	if err != nil {
		// crypto/rand does not fail on the supported systems; a fixed zone is still a name
		return "0"
	}
	return strconv.FormatInt(n.Int64(), 10)
}

// seedInstanceName draws the parts of the instance's own name that are drawn once at the first
// start (the hostname, a random zone) and saves them (CREDENTIAL_STORES.md "Two levels"). The
// active monitor only: it writes default.toml and pushes it, the standby takes the same name
// from it, so the pair share one name.
func (repman *ReplicationManager) seedInstanceName() {
	if repman.Status != ConstMonitorActif {
		return
	}
	if repman.Conf.SeedInstanceName(repman.Hostname, randomInstanceZone) {
		name := repman.Conf.InstanceSubDomain + "/" + repman.Conf.InstanceSubDomainZone
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Instance name drawn: %s (the company, instance-domain, is entered in the GUI)", name)
		repman.Save()
	}
}
