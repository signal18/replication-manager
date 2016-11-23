// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package misc

import "testing"

func TestGetLocalIP(t *testing.T) {
	ip := GetLocalIP()
	if ip == "" {
		t.Fatal("Returned empty string, expected IP")
	}
	t.Log("got Local IP:", ip)
}

func TestSplitPair(t *testing.T) {
	pwd := "root:1234#!:$abcd"
	u, p := SplitPair(pwd)
	if u != "root" || p != "1234#!:$abcd" {
		t.Fatalf("Expected root and 1234#!:$abcd, got %s and %s instead", u, p)
	}
}

func TestGetIPSafe(t *testing.T) {
	ip, err := GetIPSafe("localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("localhost got ip", ip)
	ip, err = GetIPSafe("192.168.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("192.168.0.1 got ip", ip)
}
