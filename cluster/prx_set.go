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
	"fmt"
	"os"
)

func (proxy *Proxy) SetDataDir() {
	proxy.Datadir = proxy.ClusterGroup.Conf.WorkingDir + "/" + proxy.ClusterGroup.Name + "/" + proxy.Host + "_" + proxy.Port
	if _, err := os.Stat(proxy.Datadir); os.IsNotExist(err) {
		os.MkdirAll(proxy.Datadir, os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/log", os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/var", os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/init", os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/bck", os.ModePerm)
	}
}

func (proxy *Proxy) SetProvisionCookie() {
	newFile, err := os.Create(proxy.Datadir + "/@cookie_prov")
	if err != nil {
		fmt.Println("Error:", err)
	}
	newFile.Close()
}
