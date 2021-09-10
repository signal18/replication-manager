// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"errors"
	"testing"
)

func TestStructs_Error(t *testing.T) {
	err := Error{404, errors.New("not found")}
	if err.Error() == "" {
		t.Errorf("Failed to create custom error")
	}
}
