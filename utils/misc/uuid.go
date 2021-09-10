// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"strings"

	uuid "github.com/satori/go.uuid"
)

func GetUUID() string {
	myUUID := uuid.NewV4()
	return strings.Split(myUUID.String(), "-")[0]
}
