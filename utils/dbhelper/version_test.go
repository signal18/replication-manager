// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package dbhelper

import (
	"testing"
)

func TestMySQLVersion(t *testing.T) {
	var tstring, cstring string
	tstring, cstring = "8.0.28", ""
	mv, _ := NewMySQLVersion(tstring, cstring)

	t.Logf("Created Version of %s with version %d.%d.%d", mv.Flavor, mv.Major, mv.Minor, mv.Release)

	if mv.Equal("8.0.28") {
		t.Log("Equal(8.0.28) is true (Correct)")
	} else {
		t.Error("Equal(8.0.28) is false (Incorrect)")
	}

	if mv.Equal("8.0") {
		t.Log("Equal(8.0) is true (correct)")
	} else {
		t.Error("Equal(8.0) is false (Incorrect)")
	}

	if mv.Equal("8") {
		t.Log("Equal(8) is true (correct)")
	} else {
		t.Error("Equal(8) is false (Incorrect)")
	}

	if mv.Equal("10") == false {
		t.Log("Equal(10) is false (correct)")
	} else {
		t.Error("Equal(10) is true (Incorrect)")
	}

	if mv.GreaterEqual("8.0.28") {
		t.Log("GreaterEqual(8.0.28) is true (Correct)")
	} else {
		t.Error("GreaterEqual(8.0.28) is false (Incorrect)")
	}

	if mv.GreaterEqual("8.0") {
		t.Log("GreaterEqual(8.0) is true (Correct)")
	} else {
		t.Error("GreaterEqual(8.0) is false (Incorrect)")
	}

	if mv.GreaterEqual("8.1") == false {
		t.Log("GreaterEqual(8.1) is false (Correct)")
	} else {
		t.Error("GreaterEqual(8.0) is true (Incorrect)")
	}

	if mv.Greater("8.1") == false {
		t.Log("GreaterEqual(8.1) is false (Correct)")
	} else {
		t.Error("GreaterEqual(8.1) is true (Incorrect)")
	}

	if mv.Greater("8") == false {
		t.Log("Greater(8) is false (Correct)")
	} else {
		t.Error("Greater(8) is true (Incorrect)")
	}

	if mv.Greater("5") {
		t.Log("Greater(5) is true (Correct)")
	} else {
		t.Error("Greater(5) is false (Incorrect)")
	}

	if mv.LowerEqual("8.0.28") {
		t.Log("LowerEqual(8.0.28) is true (Correct)")
	} else {
		t.Error("LowerEqual(8.0.28) is false (Incorrect)")
	}

	if mv.LowerEqual("8.0") {
		t.Log("LowerEqual(8.0) is true (Correct)")
	} else {
		t.Error("LowerEqual(8.0) is false (Incorrect)")
	}

	if mv.LowerEqual("8.1") {
		t.Log("LowerEqual(8.1) is true (Correct)")
	} else {
		t.Error("LowerEqual(8.0) is false (Incorrect)")
	}

	if mv.Lower("8.1") {
		t.Log("Lower(8.1) is true (Correct)")
	} else {
		t.Error("Lower(8.1) is false (Incorrect)")
	}

	if mv.Lower("8") == false {
		t.Log("Lower(8) is false (Correct)")
	} else {
		t.Error("Lower(8) is true (Incorrect)")
	}

	if mv.Lower("5") == false {
		t.Log("Lower(5) is false (Correct)")
	} else {
		t.Error("Lower(5) is true (Incorrect)")
	}

	if mv.Between("5", "8") {
		t.Log("Between(5,8) is true (Correct)")
	} else {
		t.Error("Between(5,8) is false (Incorrect)")
	}

	if mv.Between("10", "11") == false {
		t.Log("Between(10,11) is false (Correct)")
	} else {
		t.Error("Between(10,11) is true (Incorrect)")
	}

}

func TestMariaDBVersion(t *testing.T) {
	var tstring, cstring string
	tstring, cstring = "10.11.6-MariaDB-1:10.11.6+maria~ubu2204-log", "MariaDB"
	mv, _ := NewMySQLVersion(tstring, cstring)

	t.Logf("Created Version of %s with version %d.%d.%d", mv.Flavor, mv.Major, mv.Minor, mv.Release)

	if mv.Equal("10.11.6") {
		t.Log("Equal(10.11.6) is true (Correct)")
	} else {
		t.Error("Equal(10.11.6) is false (Incorrect)")
	}

	if mv.Equal("10.11") {
		t.Log("Equal(10.11) is true (correct)")
	} else {
		t.Error("Equal(10.11) is false (Incorrect)")
	}

	if mv.Equal("10") {
		t.Log("Equal(10) is true (correct)")
	} else {
		t.Error("Equal(10) is false (Incorrect)")
	}

	if mv.Equal("8") == false {
		t.Log("Equal(8) is false (correct)")
	} else {
		t.Error("Equal(8) is true (Incorrect)")
	}

	if mv.GreaterEqual("10.11.6") {
		t.Log("GreaterEqual(10.11.6) is true (Correct)")
	} else {
		t.Error("GreaterEqual(10.11.6) is false (Incorrect)")
	}

	if mv.GreaterEqual("10.11") {
		t.Log("GreaterEqual(10.11) is true (Correct)")
	} else {
		t.Error("GreaterEqual(10.11) is false (Incorrect)")
	}

	if mv.GreaterEqual("10.12") == false {
		t.Log("GreaterEqual(10.12) is false (Correct)")
	} else {
		t.Error("GreaterEqual(10.12) is true (Incorrect)")
	}

	if mv.Greater("10.12") == false {
		t.Log("GreaterEqual(10.12) is false (Correct)")
	} else {
		t.Error("GreaterEqual(10.12) is true (Incorrect)")
	}

	if mv.Greater("10") == false {
		t.Log("Greater(10) is false (Correct)")
	} else {
		t.Error("Greater(10) is true (Incorrect)")
	}

	if mv.Greater("5") {
		t.Log("Greater(5) is true (Correct)")
	} else {
		t.Error("Greater(5) is false (Incorrect)")
	}

	if mv.LowerEqual("10.11.6") {
		t.Log("LowerEqual(10.11.6) is true (Correct)")
	} else {
		t.Error("LowerEqual(10.11.6) is false (Incorrect)")
	}

	if mv.LowerEqual("10.11") {
		t.Log("LowerEqual(10.11) is true (Correct)")
	} else {
		t.Error("LowerEqual(10.11) is false (Incorrect)")
	}

	if mv.LowerEqual("10.12") {
		t.Log("LowerEqual(10.12) is true (Correct)")
	} else {
		t.Error("LowerEqual(10.12) is false (Incorrect)")
	}

	if mv.Lower("10.12") {
		t.Log("Lower(10.12) is true (Correct)")
	} else {
		t.Error("Lower(10.12) is false (Incorrect)")
	}

	if mv.Lower("10") == false {
		t.Log("Lower(10) is false (Correct)")
	} else {
		t.Error("Lower(10) is true (Incorrect)")
	}

	if mv.Lower("5") == false {
		t.Log("Lower(5) is false (Correct)")
	} else {
		t.Error("Lower(5) is true (Incorrect)")
	}

	if mv.Between("5", "10") {
		t.Log("Between(5,10) is true (Correct)")
	} else {
		t.Error("Between(5,10) is false (Incorrect)")
	}

	if mv.Between("5", "8") == false {
		t.Log("Between(5,8) is false (Correct)")
	} else {
		t.Error("Between(5,8) is true (Incorrect)")
	}

}
