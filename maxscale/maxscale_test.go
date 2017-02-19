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
