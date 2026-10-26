// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// #2185 review: APIUsers is read without a lock (public login routes, ACL checks) while users are
// added and dropped: every write swaps a new map in (copy on write), so a reader never meets a map
// being written. Before, an in-place write during a read stopped the process ("concurrent map
// read and map write", not a recoverable panic).
func TestAPIUsersCopyOnWrite(t *testing.T) {
	c := &Cluster{Conf: &config.Config{}, APIUsers: map[string]APIUser{"admin": {User: "admin", Password: "x", Grants: map[string]bool{"a": true}}}}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for name, u := range c.APIUsers {
					_ = c.IsLocalOnlyAccount(name)
					_ = u.Grants["a"]
				}
			}
		}()
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		name := fmt.Sprintf("u%d@x.io", i%50)
		c.setAPIUser(name, APIUser{User: name, Grants: map[string]bool{"a": true}})
		c.deleteAPIUser(fmt.Sprintf("u%d@x.io", (i+25)%50))
	}
	close(stop)
	wg.Wait()
	if _, ok := c.APIUsers["admin"]; !ok {
		t.Fatal("a write must keep the other users")
	}
}
