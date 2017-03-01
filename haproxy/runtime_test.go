// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"github.com/magneticio/vamp-router/helpers"
	"io/ioutil"
	"os"
	"os/exec"
	"testing"
)

var (
	haRuntime = Runtime{Binary: helpers.HaproxyLocation(), SockFile: "/tmp/haproxy.stat.sock"}
)

func TestRuntime_SetNewPid(t *testing.T) {

	//make sure there is no pidfile present
	os.Remove(PID_FILE)

	if err := haRuntime.SetPid(PID_FILE); err != nil {
		t.Fatalf(err.Error())
	}

}

func TestRuntime_UseExistingPid(t *testing.T) {

	//create a pid file
	emptyPid := []byte("12356")
	ioutil.WriteFile(PID_FILE, emptyPid, 0644)
	defer os.Remove(PID_FILE)

	if err := haRuntime.SetPid(PID_FILE); err == nil {
		t.Fatalf("err: Failed to read existing pid file")
	}

}

// all tests againt a running Haproxy are for now lumped together. TODO: split it up
func TestRuntime_HaproxyFunctions(t *testing.T) {

	/*
		Preamble to set up and tear down haproxy
	*/

	//create a pid file
	emptyPid := []byte("")
	ioutil.WriteFile(PID_FILE, emptyPid, 0644)
	defer os.Remove(PID_FILE)

	test_config_file, err := ioutil.ReadFile(PREFILLED_CONFIG_FILE)
	if err != nil {
		t.Fatal(err.Error())
	}

	err = ioutil.WriteFile("/tmp/haproxy_test.cfg", test_config_file, 0664)
	// defer os.Remove("/tmp/haproxy_test.cfg")
	if err != nil {
		t.Fatal(err.Error())
	}

	/*
	 Start actual tests
	*/

	//TODO: configure runtime with socket

	// run first time, pid should be empty
	haConfig.ConfigFile = "/tmp/haproxy_test.cfg"
	if err := haRuntime.Reload(&haConfig); err != nil {
		t.Fatal("failed to reload Haproxy with empty pif: " + err.Error())
	}
	defer DestroyHaproxy()

	// run it a second time, pid should be filled
	if err := haRuntime.Reload(&haConfig); err != nil {
		t.Fatal("failed to reload Haproxy with filled pid: " + err.Error())
	}

	//run it a third time with wrong config file path
	haConfig.ConfigFile = "this_is_totally_wrong"

	if err := haRuntime.Reload(&haConfig); err == nil {
		t.Fatal("There should be an error when provided with a wrong path")
	}

	// weight

	if _, err := haRuntime.SetWeight("test_be_1", "test_be_1_a", 50); err != nil {
		t.Error("failed to update weight on server")
	}

	if result, _ := haRuntime.SetWeight("test_be_1", "no_such_server", 50); result != "No such server.\n\n" {
		t.Error("should return error when setting weight on non existent server: " + result)
	}

	// acl function not yet done
	// if _, err := haRuntime.SetAcl("test_fe_bd", "test_acl_1", "hdr_sub(user-agent) MSIE"); err != nil {
	// 	t.Error("failed to set acl on server")
	// }

	// getInfo
	if _, err := haRuntime.GetInfo(); err != nil {
		t.Error("failed to info from on haproxy")
	}

	// getStats
	statsType := []string{"all", "frontend", "backend", "server"}

	for _, stat := range statsType {
		if _, err := haRuntime.GetStats(stat); err != nil {
			t.Error("failed to all stats from haproxy")
		}
	}

}

func DestroyHaproxy() {
	_ = exec.Command("killall", "haproxy").Run()
}
