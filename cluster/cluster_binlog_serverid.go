// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"sync"
	"time"
)

// Replica server-id pool for every binlog consumer of a cluster (#1886, Stéphane 2026-10-05:
// "a helper that manages concurrency on this, going from 10000 to 10010").
//
// A primary accepts ONE Binlog Dump thread per replica server_id: a second consumer
// presenting an id in use kills the first. Replication-manager streams a primary's
// binlog from several places that can overlap in time -- the security event scanner
// (persistent), the binlog metadata refresh at every rotation, the restore and
// flashback lookups, the binlog backup copy, the crash rejoin fetch -- and used to
// hand them fixed ids (10000, 12000) that collided with each other and with the same
// consumer on a sibling instance (the standby). See
// doc/implementation/cluster/BINLOG_CONSUMERS_SERVER_ID.md.
//
// Every consumer now LEASES an id from this pool for the life of its stream and
// releases it when the stream closes: concurrent consumers of one instance get distinct
// ids by construction. The pool is 11 ids, check-binlog-server-id .. +10 (10000..10010
// by default), shifted by a per-instance block derived from the hostname so the active
// and the standby never share an id either (blocks are 11 apart, 180 blocks: 10000..11979).
// An exhausted pool refuses the lease: the caller skips its run and says so, it never
// steals an id in use.

const (
	binlogServerIDPoolSize   = 11  // 10000..10010
	binlogServerIDBlockCount = 180 // per-instance blocks of poolSize: 10000..11979
)

// binlogServerIDLease is one id in use.
type binlogServerIDLease struct {
	ID      uint32    `json:"id"`
	Purpose string    `json:"purpose"` // event-scanner | binlog-meta | restore-lookup | binlog-backup | rejoin-fetch
	Since   time.Time `json:"since"`
}

type binlogServerIDPool struct {
	mu     sync.Mutex
	base   uint32
	leases map[uint32]binlogServerIDLease
}

func newBinlogServerIDPool(base uint32) *binlogServerIDPool {
	return &binlogServerIDPool{base: base, leases: make(map[uint32]binlogServerIDLease)}
}

// Acquire leases the lowest free id of the pool for purpose. The release function is
// idempotent. An exhausted pool (every id streaming) returns an error naming who holds
// them; a zero base (check-binlog-server-id 0) refuses too: go-mysql aborts on id 0.
func (p *binlogServerIDPool) Acquire(purpose string) (uint32, func(), error) {
	if p == nil || p.base == 0 {
		return 0, func() {}, fmt.Errorf("replica server-id pool disabled: check-binlog-server-id is 0")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := uint32(0); i < binlogServerIDPoolSize; i++ {
		id := p.base + i
		if _, busy := p.leases[id]; busy {
			continue
		}
		p.leases[id] = binlogServerIDLease{ID: id, Purpose: purpose, Since: time.Now()}
		var once sync.Once
		return id, func() {
			once.Do(func() {
				p.mu.Lock()
				delete(p.leases, id)
				p.mu.Unlock()
			})
		}, nil
	}
	return 0, func() {}, fmt.Errorf("replica server-id pool %d..%d exhausted for %s: %s", p.base, p.base+binlogServerIDPoolSize-1, purpose, p.describeLocked())
}

// Leases lists the ids in use, lowest first.
func (p *binlogServerIDPool) Leases() []binlogServerIDLease {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]binlogServerIDLease, 0, len(p.leases))
	for _, l := range p.leases {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Range is the first and last id of the pool.
func (p *binlogServerIDPool) Range() (uint32, uint32) {
	if p == nil {
		return 0, 0
	}
	return p.base, p.base + binlogServerIDPoolSize - 1
}

func (p *binlogServerIDPool) describeLocked() string {
	s := ""
	for _, l := range p.leases {
		s += fmt.Sprintf("%d=%s(%s) ", l.ID, l.Purpose, time.Since(l.Since).Round(time.Second))
	}
	if s == "" {
		return "no lease"
	}
	return s
}

// binlogServerIDPool returns the cluster's pool, built once from check-binlog-server-id
// and the instance block.
func (cluster *Cluster) binlogServerIDPool() *binlogServerIDPool {
	cluster.binlogServerIDsOnce.Do(func() {
		base := cluster.Conf.CheckBinServerId
		if base > 0 {
			base += binlogServerIDInstanceBlock()
		}
		cluster.binlogServerIDs = newBinlogServerIDPool(uint32(base))
	})
	return cluster.binlogServerIDs
}

var (
	binlogServerIDBlockOnce sync.Once
	binlogServerIDBlock     int
)

// binlogServerIDInstanceBlock is the per-instance shift of the pool: FNV-1a of the
// hostname folded into one of 180 blocks of poolSize ids. Two instances watching the
// same cluster (the active and the standby, a dev instance on the side) get distinct
// blocks unless their hostnames collide on the hash (1 in 180, visible in the startup
// log; set check-binlog-server-id differently on one of them then).
func binlogServerIDInstanceBlock() int {
	binlogServerIDBlockOnce.Do(func() {
		binlogServerIDBlock = binlogServerIDBlockFor(binlogServerIDInstanceName())
	})
	return binlogServerIDBlock
}

func binlogServerIDInstanceName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "replication-manager"
	}
	return h
}

func binlogServerIDBlockFor(instance string) int {
	f := fnv.New32a()
	f.Write([]byte(instance))
	return int(f.Sum32()%binlogServerIDBlockCount) * binlogServerIDPoolSize
}
