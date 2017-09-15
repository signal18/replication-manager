// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package persister

import (
	"github.com/signal18/replication-manager/graphite/helper"
	"time"
)

type ThrottleTicker struct {
	helper.Stoppable
	C chan bool
}

func NewThrottleTicker(ratePerSec int) *ThrottleTicker {
	t := &ThrottleTicker{
		C: make(chan bool, ratePerSec),
	}

	t.Start()

	if ratePerSec <= 0 {
		close(t.C)
		return t
	}

	t.Go(func(exit chan bool) {
		defer close(t.C)

		delimeter := ratePerSec
		chunk := 1

		if ratePerSec > 1000 {
			minRemainder := ratePerSec

			for i := 100; i < 1000; i++ {
				if ratePerSec%i < minRemainder {
					delimeter = i
					minRemainder = ratePerSec % delimeter
				}
			}

			chunk = ratePerSec / delimeter
		}

		step := time.Duration(1e9/delimeter) * time.Nanosecond

		ticker := time.NewTicker(step)
		defer ticker.Stop()

	LOOP:
		for {
			select {
			case <-ticker.C:
				for i := 0; i < chunk; i++ {
					select {
					case t.C <- true:
					//pass
					case <-exit:
						break LOOP
					}
				}
			case <-exit:
				break LOOP
			}
		}
	})

	return t
}
