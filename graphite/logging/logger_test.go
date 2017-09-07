// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package logging

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestSetLevel(t *testing.T) {
	assert := assert.New(t)

	table := []*struct {
		level       int
		levelString string
		checkString string
		writer      func(args ...interface{})
	}{
		{0, "debug", "_DebugMessage_", logrus.Debug},
		{1, "info", "_InfoMessage_", logrus.Info},
		{2, "warning", "_WarningMessage_", logrus.Warning},
		{2, "warn", "_WarnMessage_", logrus.Warn},
		{3, "error", "_ErrorMessage_", logrus.Error},
	}

	callLoggers := func() {
		for i := 0; i < len(table); i++ {
			table[i].writer(table[i].checkString)
		}
	}

	originalLevel := logrus.GetLevel()
	defer logrus.SetLevel(originalLevel)

	for testIndex := 0; testIndex < len(table); testIndex++ {
		checkLevel := table[testIndex].level

		TestWithLevel(table[testIndex].levelString, func(log TestOut) {
			callLoggers()

			for i := 0; i < len(table); i++ {
				if table[i].level < checkLevel {
					assert.NotContains(log.String(), table[i].checkString)
				} else {
					assert.Contains(log.String(), table[i].checkString)
				}
			}
		})
	}

	err := SetLevel("unknown")
	assert.Error(err)
}
