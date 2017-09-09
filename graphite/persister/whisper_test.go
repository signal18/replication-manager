// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package persister

import (
	"github.com/lomik/go-carbon/points"

	"math/rand"
)

func randomPoints(num int, out chan *points.Points) {
	var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	var i int
	for i = 0; i < num; i++ {
		b := make([]rune, 32)
		for i := range b {
			b[i] = letters[rand.Intn(len(letters))]
		}
		metric := string(b)
		p := points.OnePoint(metric, rand.Float64(), rand.Int63())
		out <- p
	}
}
