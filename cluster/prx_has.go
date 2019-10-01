// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import "strings"

func (proxy *Proxy) IsFilterInTags(filter string) bool {
	tags := proxy.ClusterGroup.GetProxyTags()
	for _, tag := range tags {
		if strings.Contains(filter, tag) {
			//	fmt.Println(server.ClusterGroup.Conf.ProvTags + " vs tag: " + tag + "  against " + filter)
			return true
		}
	}
	return false
}
