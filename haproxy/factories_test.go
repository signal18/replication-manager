// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"testing"
)

func TestFactories_CompileSocketName(t *testing.T) {

	dir := "/usr/home/JohnDoe/.vamp_router/sockets"
	illegal_names := []string{
		"",
		"a_much_too_long_name_that_is_actually_valid_with_regard_to_chars_but_still_kinda_ridiculous_because_of_its_obvious_length_issues",
	}

	for _, base := range illegal_names {
		if len(compileSocketName(dir, base)) > MAX_SOCKET_LENGTH {
			t.Errorf("Failed to create socketPath with less than %s characters", MAX_SOCKET_LENGTH)
		}
	}
}
