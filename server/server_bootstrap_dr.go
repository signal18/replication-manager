// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// selfAPIURL is the URL this instance writes as REPLICATION_MANAGER_URL in the
// namespaces it provisions (cluster.openSVCCreateMaps*).
func (repman *ReplicationManager) selfAPIURL() string {
	return repman.Conf.MonitorAPIURL()
}

// recordPeerAPIURL keeps the arbitration peer's API URL from its heartbeat
// answer. The URL ends up downloaded and run by init containers, so only a
// plain https://host:port whose host is the configured peer that answered is
// kept (#1942 review): a heartbeat answered by anything else cannot redirect
// the bootstraps. An answer from this very instance (the default peer
// 127.0.0.1) or without a URL (an older peer) changes nothing. Called under the
// repman lock.
func (repman *ReplicationManager) recordPeerAPIURL(peer string, h Heartbeat) {
	if h.APIURL == "" || h.UUID == repman.UUID || h.APIURL == repman.selfAPIURL() {
		return
	}
	if err := validPeerAPIURL(peer, h.APIURL); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModHeartBeat, config.LvlWarn,
			"Peer %s advertises API URL %q, not offered to bootstraps: %s", peer, h.APIURL, err)
		return
	}
	repman.peerAPIURL = h.APIURL
}

// peerURLHostChars is what a host:port may contain once offered to shell loops.
var peerURLHostChars = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$`)

// validPeerAPIURL accepts https://host[:port] with nothing else, whose host is
// the host of the configured arbitration peer entry (scheme and port ignored).
func validPeerAPIURL(peer, apiURL string) error {
	u, err := url.Parse(apiURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scheme %q, want https", u.Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !peerURLHostChars.MatchString(u.Host) {
		return fmt.Errorf("not a plain https://host:port")
	}
	peerHost := peer
	if i := strings.Index(peerHost, "://"); i >= 0 {
		peerHost = peerHost[i+3:]
	}
	peerHost = strings.SplitN(peerHost, "/", 2)[0]
	if h, _, err := net.SplitHostPort(peerHost); err == nil {
		peerHost = h
	}
	if !strings.EqualFold(u.Hostname(), peerHost) {
		return fmt.Errorf("host %q is not the configured peer %q", u.Hostname(), peerHost)
	}
	return nil
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
