// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"os"
)

func (proxy *Proxy) delCookie(key string) error {
	err := os.Remove(proxy.Datadir + "/@" + key)
	if err != nil {
		proxy.ClusterGroup.LogPrintf(LvlDbg, "Remove cookie (%s) %s", key, err)
	}

	return err
}

func (proxy *Proxy) DelProvisionCookie() error {
	return proxy.delCookie("cookie_prov")
}

func (proxy *Proxy) DelReprovisionCookie() error {
	return proxy.delCookie("cookie_reprov")
}

func (proxy *Proxy) DelRestartCookie() error {
	return proxy.delCookie("cookie_restart")
}

func (proxy *Proxy) DelWaitStartCookie() error {
	return proxy.delCookie("cookie_waitstart")
}

func (proxy *Proxy) DelWaitStopCookie() error {
	return proxy.delCookie("cookie_waitstop")
}
