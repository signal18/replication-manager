// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package maxscale

import "testing"

const MaxScaleTestIP = "172.16.1.4"

func TestConnect(t *testing.T) {
	m := MaxScale{MaxScaleTestIP, maxDefaultPort, maxDefaultUser, maxDefaultPass, nil}
	err := m.Connect()
	if err != nil {
		t.Fatal("Could not establish a connection:", err)
	}
}

func TestCommand(t *testing.T) {
	m := MaxScale{MaxScaleTestIP, maxDefaultPort, maxDefaultUser, maxDefaultPass, nil}
	m.Connect()
	err := m.Command("reload config")
	if err != nil {
		t.Fatal("Could not send command:", err)
	}
}

func TestShowServers(t *testing.T) {
	m := MaxScale{MaxScaleTestIP, maxDefaultPort, maxDefaultUser, maxDefaultPass, nil}
	m.Connect()
	response, err := m.ShowServers()
	if err != nil {
		t.Error("Failed to get server list")
	}
	if len(response) < 1 {
		t.Errorf("Received illegal response length: %d\n", len(response))
	}
	t.Log(string(response))
}

func TestListServers(t *testing.T) {
	m := MaxScale{MaxScaleTestIP, maxDefaultPort, maxDefaultUser, maxDefaultPass, nil}
	m.Connect()
	srvlist, err := m.ListServers()
	if err != nil {
		t.Error("Failed to get server list")
	}
	if len(srvlist) < 1 {
		t.Errorf("Server list is empty")
	}
	t.Log(srvlist)
}
