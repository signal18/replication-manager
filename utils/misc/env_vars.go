// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"os"
	"strconv"
)

func SetValueFromEnv(field interface{}, envVar string) {

	env := os.Getenv(envVar)
	if len(env) > 0 {

		switch v := field.(type) {
		case *int:
			*v, _ = strconv.Atoi(env)
		case *string:
			*v = env
		case *bool:
			*v, _ = strconv.ParseBool(env)
		}
	}
}
