// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

// selfAPIURL is the URL this instance writes as REPLICATION_MANAGER_URL in the
// namespaces it provisions (cluster.openSVCCreateMaps*).
func (repman *ReplicationManager) selfAPIURL() string {
	return "https://" + repman.Conf.MonitorAddress + ":" + repman.Conf.APIPort
}

// recordPeerAPIURL keeps the arbitration peer's API URL from its heartbeat
// answer. An answer from this very instance (the default peer 127.0.0.1) or
// without a URL (an older peer) changes nothing. Called under the repman lock.
func (repman *ReplicationManager) recordPeerAPIURL(h Heartbeat) {
	if h.APIURL == "" || h.UUID == repman.UUID || h.APIURL == repman.selfAPIURL() {
		return
	}
	repman.peerAPIURL = h.APIURL
}

// bootstrapPairURLs is the REPLICATION_MANAGER_URL_DR value offered to init
// containers: this instance then its peer, only with active/standby enabled and
// once the peer's URL is known. Called under the repman lock.
func (repman *ReplicationManager) bootstrapPairURLs() string {
	if !repman.Conf.Arbitration || repman.peerAPIURL == "" {
		return ""
	}
	return repman.selfAPIURL() + " " + repman.peerAPIURL
}
