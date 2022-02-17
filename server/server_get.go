// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import "github.com/signal18/replication-manager/cluster"

func (repman *ReplicationManager) getClusterByName(clname string) *cluster.Cluster {
	var c *cluster.Cluster
	repman.Lock()
	c = repman.Clusters[clname]
	repman.Unlock()
	return c
}
