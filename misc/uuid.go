// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"github.com/satori/go.uuid"
	"strings"
)

func GetUUID() string {
	myUUID := uuid.NewV4()
	return strings.Split(myUUID.String(), "-")[0]
}
