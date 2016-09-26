package maxscale

import "testing"

func TestConnect(t *testing.T) {
	m := MaxScale{"172.16.1.4", maxDefaultPort, maxDefaultUser, maxDefaultPass, nil}
	err := m.connect()
	if err != nil {
		t.Fatal("Could not establish a connection:", err)
	}
}

func TestShowServers(t *testing.T) {
	m := MaxScale{"172.16.1.4", maxDefaultPort, maxDefaultUser, maxDefaultPass, nil}
	m.connect()
	response, err := m.showServers()
	if err != nil {
		t.Error("Failed to get server list")
	}
	if len(response) < 1 {
		t.Errorf("Received illegal response length: %d\n", len(response))
	}
	t.Log(string(response))
}
